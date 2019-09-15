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
	"strings"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	networkingv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
)

var _ = Describe("IngressRuleCombiner", func() {
	It("should not affect rulesets that don't conform", func() {
		set := []networkingv1.NetworkPolicyIngressRule{
			{
				From: []networkingv1.NetworkPolicyPeer{
					{
						PodSelector: &metav1.LabelSelector{
							MatchLabels: map[string]string{
								"bar": "foo",
							},
						},
						NamespaceSelector: &metav1.LabelSelector{
							MatchLabels: map[string]string{
								"bar": "foo",
							},
						},
					},
				},
			},
			{
				From: []networkingv1.NetworkPolicyPeer{
					{
						PodSelector: &metav1.LabelSelector{
							MatchLabels: map[string]string{
								"bar": "foo",
							},
						},
						NamespaceSelector: &metav1.LabelSelector{
							MatchLabels: map[string]string{
								"bar": "foo",
							},
						},
					},
				},
			},
		}

		Expect(combineIngressRules(set...)).To(Equal(set))
	})

	It("should combine correctly", func() {
		set := []networkingv1.NetworkPolicyIngressRule{
			{
				Ports: []networkingv1.NetworkPolicyPort{
					{
						Port: &intstr.IntOrString{Type: intstr.Int, IntVal: int32(80)},
					},
				},
				From: []networkingv1.NetworkPolicyPeer{
					{
						PodSelector: &metav1.LabelSelector{
							MatchLabels: map[string]string{
								"bar": "foo",
							},
						},
					},
				},
			},
			{
				Ports: []networkingv1.NetworkPolicyPort{
					{
						Port: &intstr.IntOrString{Type: intstr.Int, IntVal: int32(80)},
					},
				},
				From: []networkingv1.NetworkPolicyPeer{
					{
						PodSelector: &metav1.LabelSelector{
							MatchLabels: map[string]string{
								"bar": "baz",
							},
						},
					},
				},
			}}

		expected := []networkingv1.NetworkPolicyIngressRule{
			{
				Ports: []networkingv1.NetworkPolicyPort{
					{
						Port: &intstr.IntOrString{Type: intstr.Int, IntVal: int32(80)},
					},
				},
				From: []networkingv1.NetworkPolicyPeer{
					{
						PodSelector: &metav1.LabelSelector{
							MatchExpressions: []metav1.LabelSelectorRequirement{
								{
									Key:      "bar",
									Operator: metav1.LabelSelectorOpIn,
									Values:   []string{"foo", "baz"},
								},
							},
						},
					},
				},
			},
		}

		Expect(combineIngressRules(set...)).To(Equal(expected))
	})

	It("should not combine if ports differ", func() {
		set := []networkingv1.NetworkPolicyIngressRule{
			{
				Ports: []networkingv1.NetworkPolicyPort{
					{
						Port: &intstr.IntOrString{Type: intstr.Int, IntVal: int32(80)},
					},
				},
				From: []networkingv1.NetworkPolicyPeer{
					{
						PodSelector: &metav1.LabelSelector{
							MatchLabels: map[string]string{
								"bar": "foo",
							},
						},
					},
				},
			},
			{
				Ports: []networkingv1.NetworkPolicyPort{
					{
						Port: &intstr.IntOrString{Type: intstr.Int, IntVal: int32(8080)},
					},
				},
				From: []networkingv1.NetworkPolicyPeer{
					{
						PodSelector: &metav1.LabelSelector{
							MatchLabels: map[string]string{
								"bar": "baz",
							},
						},
					},
				},
			}}

		Expect(combineIngressRules(set...)).To(Equal(set))
	})

	It("should not combine if label keys differ", func() {
		set := []networkingv1.NetworkPolicyIngressRule{
			{
				Ports: []networkingv1.NetworkPolicyPort{
					{
						Port: &intstr.IntOrString{Type: intstr.Int, IntVal: int32(80)},
					},
				},
				From: []networkingv1.NetworkPolicyPeer{
					{
						PodSelector: &metav1.LabelSelector{
							MatchLabels: map[string]string{
								"bar": "foo",
							},
						},
					},
				},
			},
			{
				Ports: []networkingv1.NetworkPolicyPort{
					{
						Port: &intstr.IntOrString{Type: intstr.Int, IntVal: int32(80)},
					},
				},
				From: []networkingv1.NetworkPolicyPeer{
					{
						PodSelector: &metav1.LabelSelector{
							MatchLabels: map[string]string{
								"baz": "baz",
							},
						},
					},
				},
			}}

		Expect(combineIngressRules(set...)).To(Equal(set))
	})

	Measure("It should combine lots of rules", func(b Benchmarker) {
		count := 10000

		rule := networkingv1.NetworkPolicyIngressRule{
			Ports: []networkingv1.NetworkPolicyPort{
				{
					Port: &intstr.IntOrString{Type: intstr.Int, IntVal: int32(80)},
				},
			},
			From: []networkingv1.NetworkPolicyPeer{
				{
					PodSelector: &metav1.LabelSelector{
						MatchLabels: map[string]string{
							"bar": "foo",
						},
					},
				},
			},
		}

		expected := networkingv1.NetworkPolicyIngressRule{
			Ports: []networkingv1.NetworkPolicyPort{
				{
					Port: &intstr.IntOrString{Type: intstr.Int, IntVal: int32(80)},
				},
			},
			From: []networkingv1.NetworkPolicyPeer{
				{
					PodSelector: &metav1.LabelSelector{
						MatchExpressions: []metav1.LabelSelectorRequirement{
							{
								Key:      "bar",
								Operator: metav1.LabelSelectorOpIn,
								Values:   strings.Split(strings.Repeat(",foo", count)[1:], ","),
							},
						},
					},
				},
			},
		}

		set := make([]networkingv1.NetworkPolicyIngressRule, 0, count)
		for i := 0; i < cap(set); i++ {
			set = append(set, *rule.DeepCopy())
		}

		b.Time("runtime", func() {
			output := combineIngressRules(set...)
			Expect(output).To(ConsistOf(expected))
		})
	}, 10)
})
