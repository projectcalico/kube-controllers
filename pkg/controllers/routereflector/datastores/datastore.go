// Copyright (c) 2020 IBM Corporation All rights reserved.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package datastores

import (
	apiv3 "github.com/projectcalico/libcalico-go/lib/apis/v3"
	corev1 "k8s.io/api/core/v1"
)

// Datastore defines base functionality if a datastore
type Datastore interface {
	// Removes route reflector related annotation and do data store specific extras
	RemoveRRStatus(*corev1.Node, *apiv3.Node) error
	// Adds route reflector related annotation and do data store specific extras
	AddRRStatus(*corev1.Node, *apiv3.Node) error
}

type nodeInfo interface {
	GetNodeLabel(string) (string, string)
	GetClusterID(*corev1.Node) string
}
