package converter_test

import (
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	"github.com/projectcalico/k8s-policy/pkg/converter"
	"github.com/projectcalico/libcalico-go/lib/api"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	k8sapi "k8s.io/client-go/pkg/api/v1"
	"reflect"
)

var _ = Describe("NamespaceConverter", func() {

	Context("With empty namespace object", func() {
		ns := k8sapi.Namespace{
			ObjectMeta: metav1.ObjectMeta{},
			Spec:       k8sapi.NamespaceSpec{},
			Status:     k8sapi.NamespaceStatus{},
		}

		nsConverter := converter.NewNamespaceConverter()
		policyObject, err := nsConverter.Convert(&ns)

		expectedType := reflect.TypeOf(*api.NewProfile())
		actualType := reflect.TypeOf(policyObject)

		It("the returned object must be policy object", func() {
			Expect(err).NotTo(HaveOccurred())
			Expect(policyObject).NotTo(Equal(nil))
			Expect(expectedType).Should(Equal(actualType))
		})
	})

	Context("With assigned names", func() {
		namespace := "testNamespace"
		ns := k8sapi.Namespace{
			Spec:       k8sapi.NamespaceSpec{},
			Status:     k8sapi.NamespaceStatus{},
			ObjectMeta: metav1.ObjectMeta{Name: namespace},
		}
		nsConverter := converter.NewNamespaceConverter()
		p, err := nsConverter.Convert(&ns)

		expectedName := "ns.projectcalico.org/" + namespace
		actualName := p.(api.Profile).Metadata.Name

		It("should return calico policy having expected name", func() {
			Expect(err).NotTo(HaveOccurred())
			Expect(actualName).Should(Equal(expectedName))
		})
	})

	Context("With valid namespace object", func() {
		nsConverter := converter.NewNamespaceConverter()

		It("should parse a Namespace to a Profile", func() {
			ns := k8sapi.Namespace{
				ObjectMeta: metav1.ObjectMeta{
					Name: "default",
					Labels: map[string]string{
						"foo":   "bar",
						"roger": "rabbit",
					},
					Annotations: map[string]string{},
				},
				Spec: k8sapi.NamespaceSpec{},
			}
			p, err := nsConverter.Convert(&ns)
			Expect(err).NotTo(HaveOccurred())

			// Ensure rules are correct for profile.
			inboundRules := p.(api.Profile).Spec.IngressRules
			outboundRules := p.(api.Profile).Spec.EgressRules
			Expect(len(inboundRules)).To(Equal(1))
			Expect(len(outboundRules)).To(Equal(1))

			// Ensure both inbound and outbound rules are set to allow.
			Expect(inboundRules[0]).To(Equal(api.Rule{Action: "allow"}))
			Expect(outboundRules[0]).To(Equal(api.Rule{Action: "allow"}))

			// Check labels.
			labels := p.(api.Profile).Metadata.Labels
			Expect(labels["k8s_ns/label/foo"]).To(Equal("bar"))
			Expect(labels["k8s_ns/label/roger"]).To(Equal("rabbit"))
		})

		It("should parse a Namespace to a Profile with no labels", func() {
			ns := k8sapi.Namespace{
				ObjectMeta: metav1.ObjectMeta{
					Name:        "default",
					Annotations: map[string]string{},
				},
				Spec: k8sapi.NamespaceSpec{},
			}
			p, err := nsConverter.Convert(&ns)
			Expect(err).NotTo(HaveOccurred())

			// Ensure rules are correct for profile.
			inboundRules := p.(api.Profile).Spec.IngressRules
			outboundRules := p.(api.Profile).Spec.EgressRules
			Expect(len(inboundRules)).To(Equal(1))
			Expect(len(outboundRules)).To(Equal(1))

			// Ensure both inbound and outbound rules are set to allow.
			Expect(inboundRules[0]).To(Equal(api.Rule{Action: "allow"}))
			Expect(outboundRules[0]).To(Equal(api.Rule{Action: "allow"}))

			// Check labels.
			labels := p.(api.Profile).Metadata.Labels
			Expect(len(labels)).To(Equal(0))
		})

		It("should ignore the network-policy Namespace annotation", func() {
			ns := k8sapi.Namespace{
				ObjectMeta: metav1.ObjectMeta{
					Name: "default",
					Annotations: map[string]string{
						"net.beta.kubernetes.io/network-policy": "{\"ingress\": {\"isolation\": \"DefaultDeny\"}}",
					},
				},
				Spec: k8sapi.NamespaceSpec{},
			}

			// Ensure it generates the correct Profile.
			p, err := nsConverter.Convert(&ns)
			Expect(err).NotTo(HaveOccurred())

			// Ensure rules are correct for profile.
			inboundRules := p.(api.Profile).Spec.IngressRules
			outboundRules := p.(api.Profile).Spec.EgressRules
			Expect(len(inboundRules)).To(Equal(1))
			Expect(len(outboundRules)).To(Equal(1))

			// Ensure both inbound and outbound rules are set to allow.
			Expect(inboundRules[0]).To(Equal(api.Rule{Action: "allow"}))
			Expect(outboundRules[0]).To(Equal(api.Rule{Action: "allow"}))
		})

		It("should not fail for malformed annotation", func() {
			ns := k8sapi.Namespace{
				ObjectMeta: metav1.ObjectMeta{
					Name: "default",
					Annotations: map[string]string{
						"net.beta.kubernetes.io/network-policy": "invalidJSON",
					},
				},
				Spec: k8sapi.NamespaceSpec{},
			}

			By("converting to a Profile", func() {
				_, err := nsConverter.Convert(&ns)
				Expect(err).NotTo(HaveOccurred())
			})
		})

		It("should handle a valid but not DefaultDeny annotation", func() {
			ns := k8sapi.Namespace{
				ObjectMeta: metav1.ObjectMeta{
					Name: "default",
					Annotations: map[string]string{
						"net.beta.kubernetes.io/network-policy": "{}",
					},
				},
				Spec: k8sapi.NamespaceSpec{},
			}

			By("converting to a Profile", func() {
				p, err := nsConverter.Convert(&ns)
				Expect(err).NotTo(HaveOccurred())

				// Ensure rules are correct for profile.
				inboundRules := p.(api.Profile).Spec.IngressRules
				outboundRules := p.(api.Profile).Spec.EgressRules
				Expect(len(inboundRules)).To(Equal(1))
				Expect(len(outboundRules)).To(Equal(1))

				// Ensure both inbound and outbound rules are set to allow.
				Expect(inboundRules[0]).To(Equal(api.Rule{Action: "allow"}))
				Expect(outboundRules[0]).To(Equal(api.Rule{Action: "allow"}))
			})
		})
	})
})
