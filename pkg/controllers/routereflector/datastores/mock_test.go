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
	"context"

	apiv3 "github.com/projectcalico/libcalico-go/lib/apis/v3"
	"github.com/projectcalico/libcalico-go/lib/options"
)

type mockTopology struct {
	getClusterID func() string
	getNodeLabel func() (string, string)
}

func (m mockTopology) GetClusterID(string, int64) string {
	return m.getClusterID()
}

func (m mockTopology) GetNodeLabel(string) (string, string) {
	return m.getNodeLabel()
}

type mockCalicoClient struct {
	update func(*apiv3.Node) (*apiv3.Node, error)
}

func (m mockCalicoClient) Update(_ context.Context, node *apiv3.Node, _ options.SetOptions) (*apiv3.Node, error) {
	if m.update != nil {
		return m.update(node)
	}
	return nil, nil
}
