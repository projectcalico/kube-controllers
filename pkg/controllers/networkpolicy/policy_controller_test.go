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
	"context"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	"github.com/projectcalico/kube-controllers/pkg/converter"
	v3 "github.com/projectcalico/libcalico-go/lib/apis/v3"
	"github.com/projectcalico/libcalico-go/lib/clientv3"
	"github.com/projectcalico/libcalico-go/lib/options"
	networkingv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/tools/cache"
)

var examplePolicy = &networkingv1.NetworkPolicy{
	ObjectMeta: metav1.ObjectMeta{
		Name:      "abcdef",
		Namespace: "default",
		Labels: map[string]string{
			dynamicLabel: "s-foo",
		},
	},
	Spec: networkingv1.NetworkPolicySpec{
		PodSelector: metav1.LabelSelector{MatchLabels: map[string]string{
			"foo": "bar",
		}},
		PolicyTypes: []networkingv1.PolicyType{networkingv1.PolicyTypeIngress},
		Ingress: []networkingv1.NetworkPolicyIngressRule{
			{From: []networkingv1.NetworkPolicyPeer{
				{PodSelector: &metav1.LabelSelector{
					MatchLabels: map[string]string{
						"bar": "foo",
					},
				}},
			},
			},
		},
	},
}

var examplePolicy2 = &networkingv1.NetworkPolicy{
	ObjectMeta: metav1.ObjectMeta{
		Name:      "defabc",
		Namespace: "default",
		Labels: map[string]string{
			dynamicLabel: "s-foo",
		},
	},
	Spec: networkingv1.NetworkPolicySpec{
		PodSelector: metav1.LabelSelector{MatchLabels: map[string]string{
			"foo": "bar",
		}},
		PolicyTypes: []networkingv1.PolicyType{networkingv1.PolicyTypeIngress},
		Ingress: []networkingv1.NetworkPolicyIngressRule{
			{From: []networkingv1.NetworkPolicyPeer{
				{PodSelector: &metav1.LabelSelector{
					MatchLabels: map[string]string{
						"baz": "bal",
					},
				}},
			},
			},
		},
	},
}

var combinedPolicies = &networkingv1.NetworkPolicy{
	ObjectMeta: metav1.ObjectMeta{
		Name:      "s-foo",
		Namespace: "default",
	},
	Spec: networkingv1.NetworkPolicySpec{
		PodSelector: metav1.LabelSelector{MatchLabels: map[string]string{
			"foo": "bar",
		}},
		PolicyTypes: []networkingv1.PolicyType{networkingv1.PolicyTypeIngress},
		Ingress: []networkingv1.NetworkPolicyIngressRule{
			{From: []networkingv1.NetworkPolicyPeer{
				{PodSelector: &metav1.LabelSelector{
					MatchLabels: map[string]string{
						"bar": "foo",
					},
				}},
			},
			},
			{From: []networkingv1.NetworkPolicyPeer{
				{PodSelector: &metav1.LabelSelector{
					MatchLabels: map[string]string{
						"baz": "bal",
					},
				}},
			},
			},
		},
	},
}

type fakeCalicoClient struct {
	clientv3.Interface
	clientv3.NetworkPolicyInterface
	Processed chan string
}

func (f *fakeCalicoClient) NetworkPolicies() clientv3.NetworkPolicyInterface {
	return f
}

//func (*fakeCalicoClient) Create(ctx context.Context, res *v3.NetworkPolicy, opts options.SetOptions) (*v3.NetworkPolicy, error) {
//	panic("implement me")
//}

func (*fakeCalicoClient) Update(ctx context.Context, res *v3.NetworkPolicy, opts options.SetOptions) (*v3.NetworkPolicy, error) {
	return res, nil
}

func (f *fakeCalicoClient) Delete(ctx context.Context, namespace, name string, opts options.DeleteOptions) (*v3.NetworkPolicy, error) {
	f.Processed <- name
	return &v3.NetworkPolicy{}, nil
}

func (f *fakeCalicoClient) Get(ctx context.Context, namespace, name string, opts options.GetOptions) (*v3.NetworkPolicy, error) {
	f.Processed <- name
	return &v3.NetworkPolicy{}, nil
}

func (f *fakeCalicoClient) List(ctx context.Context, opts options.ListOptions) (*v3.NetworkPolicyList, error) {
	return &v3.NetworkPolicyList{}, nil
}

//func (*fakeCalicoClient) Watch(ctx context.Context, opts options.ListOptions) (cwatch.Interface, error) {
//	panic("implement me")
//}

var _ = Describe("Informer", func() {
	var c *policyController
	var fakeWatch *watch.FakeWatcher
	stopChan := make(chan struct{})
	processed := make(chan string, 10)

	BeforeEach(func() {
		ctx := context.Background()

		fakeWatch = watch.NewFake()
		lw := &cache.ListWatch{
			ListFunc: func(options metav1.ListOptions) (object runtime.Object, e error) {
				return &networkingv1.NetworkPolicyList{}, nil
			},
			WatchFunc: func(options metav1.ListOptions) (i watch.Interface, e error) {
				return fakeWatch, nil
			},
		}
		controller := newPolicyController(ctx, lw, &fakeCalicoClient{Processed: processed})

		c = controller.(*policyController)
		go c.Run(1, "0", stopChan)
	})

	AfterEach(func() {
		close(stopChan)
		stopChan = make(chan struct{})
	})

	It("should correctly combine two policies, and handle deletion", func() {
		// add the first
		fakeWatch.Add(examplePolicy)
		// block until the cache updates
		<-processed

		dynamicName := c.resourceCache.ListKeys()[0]

		obj, ok := c.resourceCache.Get(dynamicName)
		Expect(ok).To(BeTrue())

		initialPolicy := *combinedPolicies
		initialPolicy.Spec.Ingress = initialPolicy.Spec.Ingress[:1]

		Expect(obj).To(Equal(mustConvert(&initialPolicy)))

		// add the second
		fakeWatch.Add(examplePolicy2)
		<-processed

		obj, ok = c.resourceCache.Get(dynamicName)
		Expect(ok).To(BeTrue())

		Expect(obj).To(Equal(mustConvert(combinedPolicies)))

		// delete the second
		fakeWatch.Delete(examplePolicy2)
		<-processed

		obj, ok = c.resourceCache.Get(dynamicName)
		Expect(ok).To(BeTrue())

		Expect(obj).To(Equal(mustConvert(&initialPolicy)))

		// delete the first
		fakeWatch.Delete(examplePolicy)
		<-processed

		_, ok = c.resourceCache.Get(dynamicName)
		Expect(ok).To(BeFalse())
	})

	It("should correctly account for policy updates", func() {
		fakeWatch.Add(examplePolicy)
		<-processed
		fakeWatch.Add(examplePolicy2)
		<-processed

		dynamicName := c.resourceCache.ListKeys()[0]

		updatedPolicy := *examplePolicy2
		updatedPolicy.Spec.Ingress[0].From[0].PodSelector = &metav1.LabelSelector{
			MatchLabels: map[string]string{
				"bar": "baz",
			},
		}

		fakeWatch.Modify(&updatedPolicy)
		<-processed

		obj, ok := c.resourceCache.Get(dynamicName)
		Expect(ok).To(BeTrue())

		combinedUpdated := *combinedPolicies
		combinedUpdated.Spec.Ingress[1].From[0].PodSelector = &metav1.LabelSelector{
			MatchLabels: map[string]string{
				"bar": "baz",
			},
		}

		Expect(obj).To(Equal(mustConvert(&combinedUpdated)))
	})

	It("should correctly account for policy updates that change a dynamic label", func() {
		fakeWatch.Add(examplePolicy)
		Expect(<-processed).To(Equal("knp.default.s-foo"))
		fakeWatch.Add(examplePolicy2)
		Expect(<-processed).To(Equal("knp.default.s-foo"))

		dynamicName := c.resourceCache.ListKeys()[0]

		updatedPolicy2 := *examplePolicy2
		updatedPolicy2.Labels = map[string]string{}

		// we remove the dynamic label of policy2
		fakeWatch.Modify(&updatedPolicy2)
		// handle the dynamic policy update
		Expect(<-processed).To(Equal("knp.default.s-foo"))
		// handle the nondynamic2 policy creation
		Expect(<-processed).To(Equal("knp.default.defabc"))

		obj, ok := c.resourceCache.Get(dynamicName)
		Expect(ok).To(BeTrue())

		// dynamic policy contains only policy1 stuff
		Expect(obj).To(Equal(mustConvert(combinePolicies(examplePolicy))))

		nonDynamicObj2, ok := c.resourceCache.Get("default/knp.default." + examplePolicy2.Name)
		Expect(ok).To(BeTrue())

		Expect(nonDynamicObj2).To(Equal(mustConvert(examplePolicy2)))

		// ok lets remove the policy one label
		updatedPolicy := *examplePolicy
		updatedPolicy.Labels = map[string]string{}

		// we remove the dynamic label of policy2
		fakeWatch.Modify(&updatedPolicy)
		// handle the dynamic policy deletion
		Expect(<-processed).To(Equal("knp.default.s-foo"))
		// handle the nondynamic1 policy creation
		Expect(<-processed).To(Equal("knp.default.abcdef"))

		obj, ok = c.resourceCache.Get(dynamicName)
		Expect(ok).To(BeFalse())

		Expect(c.resourceCache.ListKeys()).To(ConsistOf("default/knp.default."+examplePolicy.Name, "default/knp.default."+examplePolicy2.Name))

		nonDynamicObj, ok := c.resourceCache.Get("default/knp.default." + examplePolicy.Name)
		Expect(ok).To(BeTrue())

		Expect(nonDynamicObj).To(Equal(mustConvert(examplePolicy)))

		nonDynamicObj2, ok = c.resourceCache.Get("default/knp.default." + examplePolicy2.Name)
		Expect(ok).To(BeTrue())

		Expect(nonDynamicObj2).To(Equal(mustConvert(examplePolicy2)))

		// lets add back the policy two label
		fakeWatch.Modify(examplePolicy2)
		// handle nondynamic2 deletion
		Expect(<-processed).To(Equal("knp.default.defabc"))
		// handle dynamic creation
		Expect(<-processed).To(Equal("knp.default.s-foo"))

		nonDynamicObj2, ok = c.resourceCache.Get("default/knp.default." + examplePolicy2.Name)
		Expect(ok).To(BeFalse())

		obj, ok = c.resourceCache.Get(dynamicName)
		Expect(ok).To(BeTrue())

		// dynamic policy contains only policy2 stuff
		Expect(obj).To(Equal(mustConvert(combinePolicies(examplePolicy2))))

		nonDynamicObj, ok = c.resourceCache.Get("default/knp.default." + examplePolicy.Name)
		Expect(ok).To(BeTrue())

		Expect(nonDynamicObj).To(Equal(mustConvert(examplePolicy)))

		// lets add back the policy one label
		fakeWatch.Modify(examplePolicy)
		// handle nondynamic1 deletion
		Expect(<-processed).To(Equal("knp.default.abcdef"))
		// handle dynamic update
		Expect(<-processed).To(Equal("knp.default.s-foo"))

		nonDynamicObj, ok = c.resourceCache.Get("default/knp.default." + examplePolicy.Name)
		Expect(ok).To(BeFalse())

		nonDynamicObj2, ok = c.resourceCache.Get("default/knp.default." + examplePolicy2.Name)
		Expect(ok).To(BeFalse())

		obj, ok = c.resourceCache.Get(dynamicName)
		Expect(ok).To(BeTrue())

		// dynamic policy contains only policy1 stuff
		Expect(obj).To(Equal(mustConvert(combinedPolicies)))
	})
})

func mustConvert(obj interface{}) interface{} {
	converter := converter.NewPolicyConverter()
	converted, err := converter.Convert(obj)
	if err != nil {
		panic(err)
	}

	return converted
}
