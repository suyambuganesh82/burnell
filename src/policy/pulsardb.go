package policy

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/apache/pulsar-client-go/pulsar"
	"github.com/kafkaesque-io/burnell/src/util"

	"github.com/apex/log"
)

// the signal to track if the liveness of the reader process
type liveSignal struct{}

/**
 * Data design - we use a topic as a database table to store tenant document.
**/

// TenantPolicyHandler is the Pulsar database driver
type TenantPolicyHandler struct {
	client      pulsar.Client
	topicName   string
	tenants     map[string]TenantPlan
	tenantsLock sync.RWMutex
	logger      *log.Entry
}

//Setup sets up the database
func (s *TenantPolicyHandler) Setup() error {
	s.logger = log.WithFields(log.Fields{"app": "tenantdb"})
	s.tenants = make(map[string]TenantPlan)
	pulsarURL := util.GetConfig().PulsarURL
	s.topicName = util.AssignString(util.GetConfig().TenantManagmentTopic, "persistent://public/default/tenants-management")
	tokenStr := util.GetConfig().PulsarToken

	clientOpt := pulsar.ClientOptions{
		URL:               pulsarURL,
		OperationTimeout:  30 * time.Second,
		ConnectionTimeout: 30 * time.Second,
	}

	if tokenStr != "" {
		clientOpt.Authentication = pulsar.NewAuthenticationToken(tokenStr)
	}

	if strings.HasPrefix(pulsarURL, "pulsar+ssl://") {
		trustStore := util.GetConfig().TrustStore //"/etc/ssl/certs/ca-bundle.crt"
		if trustStore == "" {
			return fmt.Errorf("this is fatal that we are missing trustStore while pulsar+ssl is required")
		}
		clientOpt.TLSTrustCertsFilePath = trustStore
	}

	var err error
	s.client, err = pulsar.NewClient(clientOpt)
	if err != nil {
		return err
	}

	go func() {
		sig := make(chan *liveSignal)
		go s.dbListener(sig)
		for {
			select {
			case <-sig:
				go s.dbListener(sig)
			}
		}
	}()

	return nil
}

//DbListener listens db updates
func (s *TenantPolicyHandler) dbListener(sig chan *liveSignal) error {
	defer func(termination chan *liveSignal) {
		s.logger.Errorf("tenant db listener terminated")
		termination <- &liveSignal{}
	}(sig)
	s.logger.Infof("listens to tenant database changes")
	reader, err := s.client.CreateReader(pulsar.ReaderOptions{
		Topic:          s.topicName,
		StartMessageID: pulsar.EarliestMessageID(),
	})

	if err != nil {
		return err
	}
	defer reader.Close()

	ctx := context.Background()

	// infinite loop to receive messages
	for {
		data, err := reader.Next(ctx)
		if err != nil {
			log.Errorf("tenant db listener reader error %v", err)
			return err
		}
		t := TenantPlan{}
		if err = json.Unmarshal(data.Payload(), &t); err != nil {
			s.logger.Errorf("tenant unmarshal error %v", err)
		}
		s.logger.Infof("tenant %s plan %v", t.Name, t)

		s.tenantsLock.Lock()
		if t.TenantStatus != Deleted {
			s.tenants[t.Name] = t
		} else {
			delete(s.tenants, t.Name)
		}
		s.tenantsLock.Unlock()
	}
}

// UpdateTenant creates or updates a tenant plan
func (s *TenantPolicyHandler) UpdateTenant(tenantName string, tenantPlan TenantPlan) (TenantPlan, int, error) {
	existingTenant, _ := s.GetTenant(tenantName)
	tenantPlan.Name = tenantName //enforce tenant in the database record
	newPlan, err := ReconcileTenantPlan(tenantPlan, existingTenant)
	if err != nil {
		return TenantPlan{}, http.StatusUnprocessableEntity, err
	}

	updatedPlan, err := s.updateDb(newPlan)
	if err != nil {
		return TenantPlan{}, http.StatusInternalServerError, err
	}
	return updatedPlan, http.StatusOK, nil
}

// updateDb updates records directly on DB with no validation
func (s *TenantPolicyHandler) updateDb(tenantPlan TenantPlan) (TenantPlan, error) {

	producer, err := s.client.CreateProducer(pulsar.ProducerOptions{
		Topic:           s.topicName,
		DisableBatching: true,
	})
	if err != nil {
		return TenantPlan{}, err
	}
	defer producer.Close()

	tenantPlan.UpdatedAt = time.Now()
	ctx := context.Background()
	data, err := json.Marshal(tenantPlan)
	if err != nil {
		return TenantPlan{}, err
	}
	msg := pulsar.ProducerMessage{
		Payload: data,
		Key:     tenantPlan.Name,
	}

	if _, err = producer.Send(ctx, &msg); err != nil {
		return TenantPlan{}, err
	}
	producer.Flush()

	s.logger.Infof("send to Pulsar %s", tenantPlan.Name)

	s.tenantsLock.Lock()
	s.tenants[tenantPlan.Name] = tenantPlan
	s.tenantsLock.Unlock()
	return tenantPlan, nil
}

// Close closes database
func (s *TenantPolicyHandler) Close() error {
	s.client.Close()
	return nil
}

// GetTenant gets a tenant by the name
func (s *TenantPolicyHandler) GetTenant(tenantName string) (TenantPlan, error) {
	s.tenantsLock.RLock()
	defer s.tenantsLock.RUnlock()
	if t, ok := s.tenants[tenantName]; ok {
		return t, nil
	}
	return TenantPlan{}, fmt.Errorf("not found")
}

// DeleteTenant gets a tenant by the name
func (s *TenantPolicyHandler) DeleteTenant(tenantName string) (TenantPlan, error) {
	s.tenantsLock.RLock()
	t, ok := s.tenants[tenantName]
	s.tenantsLock.RUnlock()
	if !ok {
		return TenantPlan{}, fmt.Errorf("not found")
	}

	t.TenantStatus = Deleted
	if _, err := s.updateDb(t); err != nil {
		return TenantPlan{}, err
	}

	s.tenantsLock.Lock()
	delete(s.tenants, tenantName)
	s.tenantsLock.Unlock()
	return t, nil
}

// ReconcileTenantPlan reconcile tenant plan with the requested and existing plan in the database
func ReconcileTenantPlan(reqPlan, existingPlan TenantPlan) (TenantPlan, error) {
	reqPlan.UpdatedAt = time.Now()
	emptyPlan := TenantPlan{}
	emptyPolicy := PlanPolicy{}
	reqPlanPolicy := getPlanPolicy(strings.ToLower(reqPlan.PlanType))
	if reqPlanPolicy == nil {
		return TenantPlan{}, fmt.Errorf("a valid plan type is missing")
	}

	if emptyPlan == existingPlan {
		// this is new creation
		if reqPlan.Audit == "" {
			reqPlan.Audit = "initial creation,"
		}
		if reqPlan.Policy == emptyPolicy {
			reqPlan.Policy = *reqPlanPolicy
		}
		reqPlan.TenantStatus = takeTenantStatus(reqPlan.TenantStatus, Activated)
		return reqPlan, nil
	}

	reqPlan.Policy.NumOfTopics = takeNonZero(reqPlan.Policy.NumOfTopics, existingPlan.Policy.NumOfTopics)
	reqPlan.Policy.NumOfNamespaces = takeNonZero(reqPlan.Policy.NumOfNamespaces, existingPlan.Policy.NumOfNamespaces)
	reqPlan.Policy.NumOfProducers = takeNonZero(reqPlan.Policy.NumOfProducers, existingPlan.Policy.NumOfProducers)
	reqPlan.Policy.NumOfConsumers = takeNonZero(reqPlan.Policy.NumOfConsumers, existingPlan.Policy.NumOfConsumers)
	reqPlan.Policy.Functions = takeNonZero(reqPlan.Policy.Functions, existingPlan.Policy.Functions)
	reqPlan.Policy.Name = util.AssignString(reqPlan.Policy.Name, existingPlan.Policy.Name)
	reqPlan.Policy.FeatureCodes = util.AssignString(reqPlan.Policy.FeatureCodes, existingPlan.Policy.FeatureCodes)

	if reqPlan.Policy.MessageHourRetention == 0 {
		reqPlan.Policy.MessageHourRetention = existingPlan.Policy.MessageHourRetention
	}
	reqPlan.Policy.MessageRetention = time.Duration(reqPlan.Policy.MessageHourRetention) * time.Hour

	reqPlan.TenantStatus = takeTenantStatus(reqPlan.TenantStatus, existingPlan.TenantStatus)
	reqPlan.Org = util.AssignString(reqPlan.Org, existingPlan.Org)
	reqPlan.Users = util.AssignString(reqPlan.Users, existingPlan.Users)

	reqPlan.Audit = existingPlan.Audit + "," + reqPlan.Audit
	return reqPlan, nil

}

func takeNonZero(a, b int) int {
	if a == 0 {
		return b
	}
	return a
}

func takeTenantStatus(a, b TenantStatus) TenantStatus {
	if a == Reserved0 {
		return b
	}
	return a
}