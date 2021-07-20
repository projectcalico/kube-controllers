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

package topologies

import (
	"testing"

	apiv3 "github.com/projectcalico/libcalico-go/lib/apis/v3"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestFindBGPPeerFound(t *testing.T) {
	name := "looking-for"
	peers := []*apiv3.BGPPeer{
		{},
		{
			ObjectMeta: metav1.ObjectMeta{
				Name: name,
			},
		},
	}

	peer := findBGPPeer(peers, name)

	if peer == nil {
		t.Errorf("Unable to find peer %s", name)
	} else if peer.GetName() != name {
		t.Errorf("Found the wrong peer, not %s", name)
	}
}

func TestCollectNodeInfo(t *testing.T) {
	nodes := map[*corev1.Node]bool{
		{}: false,
		{}: true,
		{ObjectMeta: metav1.ObjectMeta{UID: "rr"}}: false,
		{ObjectMeta: metav1.ObjectMeta{UID: "rr"}}: true,
	}

	actualRRs := countActiveRouteReflectors(func(UID string, _ map[string]string) bool {
		return UID == "rr"
	}, nodes)

	if actualRRs != 1 {
		t.Errorf("Actual RRs are wrong %d != %d", 1, actualRRs)
	}
}
