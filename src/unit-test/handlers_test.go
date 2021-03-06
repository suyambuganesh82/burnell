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

package tests

import (
	"testing"

	. "github.com/datastax/burnell/src/route"
)

func TestSubjectMatch(t *testing.T) {
	assert(t, VerifySubject("chris-datastax", "chris-datastax-12345qbc"), "")
	assert(t, VerifySubject("chris-datastax", "chris-datastax-client-12345qbc"), "")
	assert(t, VerifySubject("chris-datastax-client", "chris-datastax-client-client-12345qbc"), "")
	assert(t, VerifySubject("chris-datastax-client", "chris-datastax-client-admin-12345qbc"), "")
	assert(t, VerifySubject("chris-datastax", "chris-datastax-admin-12345qbc"), "")
	assert(t, VerifySubject("your-framework-dev", "your-framework-dev-admin-8e5f5b7412345"), "")
	assert(t, !VerifySubject("your-framework-dev", "your-framework-dev-adMin-8e5f5b7412345"), "")

	assert(t, !VerifySubject("chris-datastax", "chris-datastax"), "")
	assert(t, !VerifySubject("chris-datastax", "chris-datastax-client-client-12345qbc"), "")
	assert(t, !VerifySubject("chris-datastax-client", "chris-datastax-client-client-client-12345qbc"), "")
	assert(t, !VerifySubject("chris-kafkaesque", "chris-datastax-12345qbc"), "")

	t1, t2 := ExtractTenant("chris-datastax-12345qbc")
	equals(t, t1, t2)

	t1, t2 = ExtractTenant("adminuser")
	equals(t, t1, t2)
	equals(t, t1, "adminuser")

	t1, t2 = ExtractTenant("chris-datastax-client-12345qbc")
	equals(t, t1, "chris-datastax-client")
	equals(t, t2, "chris-datastax")

	t1, t2 = ExtractTenant("your-framework-dev-admin-8e5f5b7412345")
	equals(t, t1, "your-framework-dev-admin")
	equals(t, t2, "your-framework-dev")

	t1, t2 = ExtractTenant("chris-datastax-client-client-12345qbc")
	equals(t, t1, "chris-datastax-client-client")
	equals(t, t2, "chris-datastax-client")

	t1, t2 = ExtractTenant("chris-datastax-clien-12345qbc")
	equals(t, t1, "chris-datastax-clien")
	equals(t, t1, t2)

}
