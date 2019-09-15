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
	"strings"

	v1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// combineIngressRules and combineEgressRules are able to combine two rules where both allow a single peer,
// identified by the same singular label key, but with potentially different values.

func combineIngressRules(rules ...v1.NetworkPolicyIngressRule) []v1.NetworkPolicyIngressRule {
	if len(rules) < 2 {
		return rules
	}

	combinable := map[string]*v1.NetworkPolicyIngressRule{}
	results := make([]v1.NetworkPolicyIngressRule, 0, len(rules))

	for _, rule := range rules {
		switch {
		case
			len(rule.From) != 1,
			rule.From[0].NamespaceSelector != nil,
			rule.From[0].IPBlock != nil,
			rule.From[0].PodSelector == nil,
			len(rule.From[0].PodSelector.MatchExpressions) > 0,
			len(rule.From[0].PodSelector.MatchLabels) != 1:
			// We can't combine these, just add straight to results
			results = append(results, rule)
			continue
		}

		label, value := getFirstEntry(rule.From[0].PodSelector.MatchLabels)
		ports := portsToString(rule.Ports)

		if existing, ok := combinable[label+ports]; ok {
			if existing.From[0].PodSelector.MatchLabels != nil {
				existing.From[0].PodSelector.MatchExpressions = []metav1.LabelSelectorRequirement{
					{
						Key:      label,
						Operator: metav1.LabelSelectorOpIn,
						Values:   []string{existing.From[0].PodSelector.MatchLabels[label]},
					},
				}
				existing.From[0].PodSelector.MatchLabels = nil
			}

			existing.From[0].PodSelector.MatchExpressions[0].Values = append(existing.From[0].PodSelector.MatchExpressions[0].Values, value)
			continue
		}

		results = append(results, rule)
		combinable[label+ports] = &results[len(results)-1]
	}

	return results
}

func combineEgressRules(rules ...v1.NetworkPolicyEgressRule) []v1.NetworkPolicyEgressRule {
	if len(rules) < 2 {
		return rules
	}

	combinable := map[string]*v1.NetworkPolicyEgressRule{}
	results := make([]v1.NetworkPolicyEgressRule, 0, len(rules))

	for _, rule := range rules {
		switch {
		case
			len(rule.To) != 1,
			rule.To[0].NamespaceSelector != nil,
			rule.To[0].IPBlock != nil,
			rule.To[0].PodSelector == nil,
			len(rule.To[0].PodSelector.MatchExpressions) > 0,
			len(rule.To[0].PodSelector.MatchLabels) != 1:
			// We can't combine these, just add straight to results
			results = append(results, rule)
			continue
		}

		label, value := getFirstEntry(rule.To[0].PodSelector.MatchLabels)
		ports := portsToString(rule.Ports)

		if existing, ok := combinable[label+ports]; ok {
			if existing.To[0].PodSelector.MatchLabels != nil {
				existing.To[0].PodSelector.MatchExpressions = []metav1.LabelSelectorRequirement{
					{
						Key:      label,
						Operator: metav1.LabelSelectorOpIn,
						Values:   []string{existing.To[0].PodSelector.MatchLabels[label]},
					},
				}
				existing.To[0].PodSelector.MatchLabels = nil
			}

			existing.To[0].PodSelector.MatchExpressions[0].Values = append(existing.To[0].PodSelector.MatchExpressions[0].Values, value)
			continue
		}

		results = append(results, rule)
		combinable[label+ports] = &results[len(results)-1]
	}

	return results
}

func getFirstEntry(m map[string]string) (string, string) {
	for k, v := range m {
		return k, v
	}

	panic("called with empty map")
}

func portsToString(ports []v1.NetworkPolicyPort) (res string) {
	strs := make([]string, len(ports))
	for _, port := range ports {
		strs = append(strs, port.String())
	}
	sort.Strings(strs)
	return strings.Join(strs, "")
}
