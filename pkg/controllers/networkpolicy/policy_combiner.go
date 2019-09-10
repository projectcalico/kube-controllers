// Copyright (c) 2017 Tigera, Inc. All rights reserved.
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

package networkpolicy

import (
	"sort"

	networkingv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func combinePolicies(nps ...*networkingv1.NetworkPolicy) *networkingv1.NetworkPolicy {
	// Policies should be combined in alphabetical order, as felix will execute policies of the same Order in
	// alphabetical order. Furthermore we should be deterministic to avoid churn when reprocessing in a different
	// order.

	sort.Slice(nps, func(i, j int) bool {
		return nps[i].Name < nps[j].Name
	})

	base := &networkingv1.NetworkPolicy{
		ObjectMeta: metav1.ObjectMeta{
			Name:      nps[0].Labels[dynamicLabel],
			Namespace: nps[0].Namespace,
		},
		Spec: networkingv1.NetworkPolicySpec{
			PodSelector: nps[0].Spec.PodSelector,
			PolicyTypes: nps[0].Spec.PolicyTypes,
		},
	}

	for _, np := range nps {
		base.Spec.Ingress = append(base.Spec.Ingress, np.Spec.Ingress...)
		base.Spec.Egress = append(base.Spec.Egress, np.Spec.Egress...)
	}

	return base
}

func interfacesToNetPols(is []interface{}) []*networkingv1.NetworkPolicy {
	nps := make([]*networkingv1.NetworkPolicy, 0, len(is))
	for _, i := range is {
		nps = append(nps, i.(*networkingv1.NetworkPolicy))
	}
	return nps
}

func combinerIndexFunc(obj interface{}) ([]string, error) {
	np := obj.(*networkingv1.NetworkPolicy)

	if ann, ok := np.Labels[dynamicLabel]; ok {
		return []string{ann + np.Spec.PodSelector.String() + np.Namespace}, nil
	}

	return nil, nil
}
