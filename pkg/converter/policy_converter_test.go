package converter_test

import (
	"github.com/projectcalico/k8s-policy/pkg/converter"
	"github.com/projectcalico/libcalico-go/lib/api"
	"github.com/projectcalico/libcalico-go/lib/numorstring"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	extensions "k8s.io/client-go/pkg/apis/extensions/v1beta1"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
)

var _ = Describe("PolicyConverter", func() {

	npConverter := converter.NewPolicyConverter()

	It("should parse a basic NetworkPolicy to a Policy", func() {
		port80 := intstr.FromInt(80)
		np := extensions.NetworkPolicy{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "testPolicy",
				Namespace: "default",
			},
			Spec: extensions.NetworkPolicySpec{
				PodSelector: metav1.LabelSelector{
					MatchLabels: map[string]string{
						"label":  "value",
						"label2": "value2",
					},
				},
				Ingress: []extensions.NetworkPolicyIngressRule{
					{
						Ports: []extensions.NetworkPolicyPort{
							{Port: &port80},
						},
						From: []extensions.NetworkPolicyPeer{
							{
								PodSelector: &metav1.LabelSelector{
									MatchLabels: map[string]string{
										"k":  "v",
										"k2": "v2",
									},
								},
							},
						},
					},
				},
			},
		}

		// Parse the policy.
		pol, err := npConverter.Convert(&np)
		Expect(err).NotTo(HaveOccurred())

		// Assert key fields are correct.
		Expect(pol.(api.Policy).Metadata.Name).To(Equal("knp.default.default.testPolicy"))

		// Assert value fields are correct.
		Expect(int(*pol.(api.Policy).Spec.Order)).To(Equal(1000))
		// Check the selector is correct, and that the matches are sorted.
		Expect(pol.(api.Policy).Spec.Selector).To(Equal(
			"calico/k8s_ns == 'default' && label == 'value' && label2 == 'value2'"))
		protoTCP := numorstring.ProtocolFromString("tcp")
		Expect(pol.(api.Policy).Spec.IngressRules).To(ConsistOf(api.Rule{
			Action:      "allow",
			Protocol:    &protoTCP, // Defaulted to TCP.
			Source:      api.EntityRule{Selector: "calico/k8s_ns == 'default' && k == 'v' && k2 == 'v2'"},
			Destination: api.EntityRule{Ports: []numorstring.Port{numorstring.SinglePort(80)}},
		}))

		// There should be no OutboundRules
		Expect(len(pol.(api.Policy).Spec.EgressRules)).To(Equal(0))

		// Check that Types field exists and has only 'ingress'
		Expect(len(pol.(api.Policy).Spec.Types)).To(Equal(1))
		var policyType api.PolicyType = "ingress"
		Expect(pol.(api.Policy).Spec.Types[0]).To(Equal(policyType))
	})

	It("should parse a NetworkPolicy with no rules", func() {
		np := extensions.NetworkPolicy{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "testPolicy",
				Namespace: "default",
			},
			Spec: extensions.NetworkPolicySpec{
				PodSelector: metav1.LabelSelector{
					MatchLabels: map[string]string{"label": "value"},
				},
			},
		}

		// Parse the policy.
		pol, err := npConverter.Convert(&np)
		Expect(err).NotTo(HaveOccurred())

		// Assert key fields are correct.
		Expect(pol.(api.Policy).Metadata.Name).To(Equal("knp.default.default.testPolicy"))

		// Assert value fields are correct.
		Expect(int(*pol.(api.Policy).Spec.Order)).To(Equal(1000))
		Expect(pol.(api.Policy).Spec.Selector).To(Equal(
			"calico/k8s_ns == 'default' && label == 'value'"))
		Expect(len(pol.(api.Policy).Spec.IngressRules)).To(Equal(0))

		// There should be no OutboundRules
		Expect(len(pol.(api.Policy).Spec.EgressRules)).To(Equal(0))

		// Check that Types field exists and has only 'ingress'
		Expect(len(pol.(api.Policy).Spec.Types)).To(Equal(1))
		var policyType api.PolicyType = "ingress"
		Expect(pol.(api.Policy).Spec.Types[0]).To(Equal(policyType))
	})

	It("should parse a NetworkPolicy with empty podSelector", func() {
		np := extensions.NetworkPolicy{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "testPolicy",
				Namespace: "default",
			},
			Spec: extensions.NetworkPolicySpec{
				PodSelector: metav1.LabelSelector{},
			},
		}

		// Parse the policy.
		pol, err := npConverter.Convert(&np)
		Expect(err).NotTo(HaveOccurred())

		// Assert key fields are correct.
		Expect(pol.(api.Policy).Metadata.Name).To(Equal("knp.default.default.testPolicy"))

		// Assert value fields are correct.
		Expect(int(*pol.(api.Policy).Spec.Order)).To(Equal(1000))
		Expect(pol.(api.Policy).Spec.Selector).To(Equal("calico/k8s_ns == 'default'"))
		Expect(len(pol.(api.Policy).Spec.IngressRules)).To(Equal(0))

		// There should be no OutboundRules
		Expect(len(pol.(api.Policy).Spec.EgressRules)).To(Equal(0))

		// Check that Types field exists and has only 'ingress'
		Expect(len(pol.(api.Policy).Spec.Types)).To(Equal(1))
		var policyType api.PolicyType = "ingress"
		Expect(pol.(api.Policy).Spec.Types[0]).To(Equal(policyType))
	})

	It("should parse a NetworkPolicy with an empty namespaceSelector", func() {
		np := extensions.NetworkPolicy{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "testPolicy",
				Namespace: "default",
			},
			Spec: extensions.NetworkPolicySpec{
				PodSelector: metav1.LabelSelector{
					MatchLabels: map[string]string{"label": "value"},
				},
				Ingress: []extensions.NetworkPolicyIngressRule{
					extensions.NetworkPolicyIngressRule{
						From: []extensions.NetworkPolicyPeer{
							extensions.NetworkPolicyPeer{
								NamespaceSelector: &metav1.LabelSelector{
									MatchLabels: map[string]string{},
								},
							},
						},
					},
				},
			},
		}

		// Parse the policy.
		pol, err := npConverter.Convert(&np)
		Expect(err).NotTo(HaveOccurred())

		// Assert key fields are correct.
		Expect(pol.(api.Policy).Metadata.Name).To(Equal("knp.default.default.testPolicy"))

		// Assert value fields are correct.
		Expect(int(*pol.(api.Policy).Spec.Order)).To(Equal(1000))
		Expect(pol.(api.Policy).Spec.Selector).To(Equal(
			"calico/k8s_ns == 'default' && label == 'value'"))
		Expect(len(pol.(api.Policy).Spec.IngressRules)).To(Equal(1))
		Expect(pol.(api.Policy).Spec.IngressRules[0].Source.Selector).To(Equal("has(calico/k8s_ns)"))

		// There should be no OutboundRules
		Expect(len(pol.(api.Policy).Spec.EgressRules)).To(Equal(0))

		// Check that Types field exists and has only 'ingress'
		Expect(len(pol.(api.Policy).Spec.Types)).To(Equal(1))
		var policyType api.PolicyType = "ingress"
		Expect(pol.(api.Policy).Spec.Types[0]).To(Equal(policyType))
	})
})
