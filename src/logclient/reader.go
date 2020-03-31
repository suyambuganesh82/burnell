package logclient

import (
	"context"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/apache/pulsar-client-go/pulsar"
	"github.com/golang/protobuf/proto"
	"google.golang.org/grpc"

	"github.com/kafkaesque-io/burnell/src/logstream"
	"github.com/kafkaesque-io/burnell/src/pb"
	"github.com/kafkaesque-io/burnell/src/util"
)

// FunctionLogResponse is HTTP response object
type FunctionLogResponse struct {
	Logs             string
	BackwardPosition int64
	ForwardPosition  int64
}

// FunctionType is the object encapsulates all the function attributes
type FunctionType struct {
	Tenant           string
	Namespace        string
	FunctionName     string
	FunctionWorkerID string
	InputTopics      []string
	InputTopicRegex  string
	SinkTopic        string
	LogTopic         string
	AutoAck          bool
	Parallism        int32
}

// the signal to track if the liveness of the reader process
type liveSignal struct{}

// functionMap stores FunctionType object and the key is tenant+namespace+function name
var functionMap map[string]FunctionType
var fnMpLock = sync.RWMutex{}

// ReadFunctionMap reads a thread safe map
func ReadFunctionMap(key string) (FunctionType, bool) {
	fnMpLock.RLock()
	defer fnMpLock.RUnlock()
	f, ok := functionMap[key]
	return f, ok
}

// WriteFunctionMap writes a key/value to a thread safe map
func WriteFunctionMap(key string, f FunctionType) {
	fnMpLock.Lock()
	defer fnMpLock.Unlock()
	functionMap[key] = f
}

// DeleteFunctionMap deletes a key from a thread safe map
func DeleteFunctionMap(key string) bool {
	fnMpLock.Lock()
	defer fnMpLock.Unlock()
	if _, ok := functionMap[key]; ok {
		delete(functionMap, key)
		return ok
	}
	return false
}

// ReaderLoop continuously reads messages from function metadata topic
func ReaderLoop(sig chan *liveSignal) {
	defer func(s chan *liveSignal) { s <- &liveSignal{} }(sig)
	functionMap = make(map[string]FunctionType)
	fmt.Println("Pulsar Reader")

	// Configuration variables pertaining to this reader
	tokenStr := util.GetConfig().PulsarToken
	uri := util.GetConfig().PulsarURL
	// RHEL CentOS:
	trustStore := util.AssignString(util.GetConfig().CertStore, "/etc/ssl/certs/ca-bundle.crt")
	// Debian Ubuntu:
	// trustStore := '/etc/ssl/certs/ca-certificates.crt'
	topicName := "persistent://public/functions/metadata"
	token := pulsar.NewAuthenticationToken(tokenStr)

	// Pulsar client
	client, err := pulsar.NewClient(pulsar.ClientOptions{
		URL:                   uri,
		Authentication:        token,
		TLSTrustCertsFilePath: trustStore,
	})

	if err != nil {
		log.Println(err)
		return
	}

	defer client.Close()

	reader, err := client.CreateReader(pulsar.ReaderOptions{
		Topic:          topicName,
		StartMessageID: pulsar.EarliestMessageID(),
	})

	if err != nil {
		log.Println(err)
		return
	}

	defer reader.Close()

	ctx := context.Background()

	// infinite loop to receive messages
	for {
		msg, err := reader.Next(ctx)
		if err != nil {
			log.Println(err)
			return
		}
		sr := pb.ServiceRequest{}
		// fmt.Printf("Received message : %v", string(msg.Payload()))
		proto.Unmarshal(msg.Payload(), &sr)
		ParseServiceRequest(sr.GetFunctionMetaData(), sr.GetWorkerId(), sr.GetServiceRequestType())
	}

}

// ParseServiceRequest build a Function object based on Pulsar function metadata message
func ParseServiceRequest(sr *pb.FunctionMetaData, workerID string, serviceType pb.ServiceRequest_ServiceRequestType) {
	fd := sr.FunctionDetails
	key := fd.GetTenant() + fd.GetNamespace() + fd.GetName()
	if serviceType == pb.ServiceRequest_DELETE {
		DeleteFunctionMap(key)
	} else {
		f := FunctionType{
			Tenant:           fd.GetTenant(),
			Namespace:        fd.GetNamespace(),
			FunctionName:     fd.GetName(),
			FunctionWorkerID: workerID,
			InputTopicRegex:  fd.Source.GetTopicsPattern(),
			SinkTopic:        fd.Sink.Topic,
			LogTopic:         fd.GetLogTopic(),
			AutoAck:          fd.GetAutoAck(),
			Parallism:        fd.GetParallelism(),
		}
		for k := range fd.Source.InputSpecs {
			f.InputTopics = append(f.InputTopics, k)
		}
		if len(fd.Source.TopicsPattern) > 0 {
			f.InputTopics = append(f.InputTopics, fd.Source.TopicsPattern)
		}
		WriteFunctionMap(key, f)
	}
}

// FunctionTopicWatchDog is a watch dog for the function topic reader process
func FunctionTopicWatchDog() {

	go func() {
		s := make(chan *liveSignal)
		ReaderLoop(s)
		for {
			select {
			case <-s:
				ReaderLoop(s)
			}
		}
	}()
}

// GetFunctionLog gets the logs from the funcion worker process
// Since the function may get reassigned after restart, we will establish the connection every time the log request is being made.
func GetFunctionLog(functionName string, rd string) (FunctionLogResponse, error) {
	// var funcWorker string
	function, ok := ReadFunctionMap(functionName)
	if !ok {
		return FunctionLogResponse{}, fmt.Errorf("not found")
	}
	// Set up a connection to the server.
	address := function.FunctionWorkerID + logstream.LogServerPort
	// fmt.Printf("found function %s\n", address)
	address = logstream.LogServerPort
	conn, err := grpc.Dial(address, grpc.WithInsecure(), grpc.WithBlock())
	if err != nil {
		return FunctionLogResponse{}, err
	}
	defer conn.Close()
	c := logstream.NewLogStreamClient(conn)

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	req := &logstream.ReadRequest{
		File:      logstream.FunctionLogPath(function.Tenant, function.Namespace, function.FunctionName, 0),
		Direction: requestDirection(rd),
		Bytes:     500,
	}
	res, err := c.Read(ctx, req)
	if err != nil {
		log.Fatalf("could not greet: %v", err)
	}
	// log.Printf("logs: %s %v %v", res.GetLogs(), res.GetBackwardIndex(), res.GetForwardIndex())
	return FunctionLogResponse{
		Logs:             res.GetLogs(),
		BackwardPosition: res.GetBackwardIndex(),
		ForwardPosition:  res.GetForwardIndex(),
	}, nil
}

func requestDirection(r string) logstream.ReadRequest_Direction {
	if strings.TrimSpace(r) == "forward" {
		return logstream.ReadRequest_FORWARD
	}
	return logstream.ReadRequest_BACKWARD
}

// /pulsar/logs/functions/ming-luo/namespace2/for-monitor-function
