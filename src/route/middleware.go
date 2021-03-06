 //
 //  Copyright (c) 2021 Datastax, Inc.
 //  
 //  Licensed to the Apache Software Foundation (ASF) under one
 //  or more contributor license agreements.  See the NOTICE file
 //  distributed with this work for additional information
 //  regarding copyright ownership.  The ASF licenses this file
 //  to you under the Apache License, Version 2.0 (the
 //  "License"); you may not use this file except in compliance
 //  with the License.  You may obtain a copy of the License at
 //  
 //     http://www.apache.org/licenses/LICENSE-2.0
 //  
 //  Unless required by applicable law or agreed to in writing,
 //  software distributed under the License is distributed on an
 //  "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY
 //  KIND, either express or implied.  See the License for the
 //  specific language governing permissions and limitations
 //  under the License.
 //

package route

//middleware includes auth, rate limit, and etc.
import (
	"net/http"
	"strings"

	"github.com/apex/log"
	"github.com/datastax/burnell/src/util"
	"github.com/gorilla/mux"
)

// Rate is the default global rate limit
// This rate only limits the rate hitting on endpoint
// It does not limit the underline resource access
var Rate = NewSema(200)

// AuthVerifyJWT Authenticate middleware function that extracts the subject in JWT
func AuthVerifyJWT(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !util.IsPulsarJWTEnabled() {
			r.Header.Set(injectedSubs, util.DummySuperRole)
			next.ServeHTTP(w, r)
			return
		}
		tokenStr := strings.TrimSpace(strings.Replace(r.Header.Get("Authorization"), "Bearer", "", 1))
		subjects, err := util.JWTAuth.GetTokenSubject(tokenStr)

		if err == nil {
			log.Infof("Authenticated with subjects %s", subjects)
			r.Header.Set(injectedSubs, subjects)
			next.ServeHTTP(w, r)
		} else {
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
		}

	})
}

// AuthVerifyTenantJWT Authenticate middleware function that extracts the subject in JWT
func AuthVerifyTenantJWT(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !util.IsPulsarJWTEnabled() {
			r.Header.Set(injectedSubs, util.DummySuperRole)
			next.ServeHTTP(w, r)
			return
		}
		tokenStr := strings.TrimSpace(strings.Replace(r.Header.Get("Authorization"), "Bearer", "", 1))
		subjects, err := util.JWTAuth.GetTokenSubject(tokenStr)

		if err != nil {
			http.Error(w, "failed to obtain subject", http.StatusUnauthorized)
			return
		}

		log.Infof("Authenticated with subjects %s to match tenant", subjects)
		r.Header.Set(injectedSubs, subjects)
		vars := mux.Vars(r)
		if tenantName, ok := vars["tenant"]; ok {
			if VerifySubject(tenantName, subjects) {
				next.ServeHTTP(w, r)
				return
			}
			log.Errorf("Authenticated subjects %s does not match tenant %s", subjects, tenantName)
		}
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return

	})
}

// SuperRoleRequired ensures token has the super user subject
func SuperRoleRequired(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !util.IsPulsarJWTEnabled() {
			r.Header.Set(injectedSubs, util.DummySuperRole)
			next.ServeHTTP(w, r)
			return
		}
		tokenStr := strings.TrimSpace(strings.Replace(r.Header.Get("Authorization"), "Bearer", "", 1))
		subject, err := util.JWTAuth.GetTokenSubject(tokenStr)

		if err == nil && util.StrContains(util.SuperRoles, subject) {
			log.Infof("superroles Authenticated")
			next.ServeHTTP(w, r)
		} else {
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
		}

	})
}

// AuthHeaderRequired is a very weak auth to verify token existence only.
func AuthHeaderRequired(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tokenStr := strings.TrimSpace(strings.Replace(r.Header.Get("Authorization"), "Bearer", "", 1))

		if len(tokenStr) > 1 {
			next.ServeHTTP(w, r)
		} else {
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
		}

	})
}

// NoAuth bypasses the auth middleware
func NoAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		next.ServeHTTP(w, r)
	})
}

// LimitRate rate limites against http handler
// use semaphore as a simple rate limiter
func LimitRate(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		err := Rate.Acquire()
		if err != nil {
			http.Error(w, "Too many requests", http.StatusTooManyRequests)
		} else {
			next.ServeHTTP(w, r)
		}
		Rate.Release()
	})
}

// ResponseJSONContentType sets JSON as the response content type
func ResponseJSONContentType(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		next.ServeHTTP(w, r)
	})
}
