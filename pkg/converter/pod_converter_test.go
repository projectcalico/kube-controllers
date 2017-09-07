package converter

import (
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	"github.com/projectcalico/libcalico-go/lib/api"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	k8sapi "k8s.io/client-go/pkg/api/v1"
)

var _ = Describe("PodConverter", func() {

	wepConverter := NewPodConverter()

	Context("Pod with no labels", func() {
		pod := k8sapi.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "podA",
				Namespace: "default",
			},
			Spec: k8sapi.PodSpec{
				NodeName: "nodeA",
			},
		}

		wep, err := wepConverter.Convert(&pod)
		It("should not generate a conversion error", func() {
			Expect(err).NotTo(HaveOccurred())
		})

		// Assert workloadID.
		It("should return workloadendpoint with correct workloadID", func() {
			Expect(wep.(api.WorkloadEndpoint).Metadata.Workload).To(Equal("default.podA"))
		})

		// Assert labels.
		It("should return workloadendpoint with namespace label", func() {
			Expect(wep.(api.WorkloadEndpoint).Metadata.Labels).To(Equal(map[string]string{"calico/k8s_ns": "default"}))
		})
	})

	Context("Pod with labels", func() {
		pod := k8sapi.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "podA",
				Namespace: "default",
				Labels: map[string]string{
					"foo":   "bar",
					"roger": "rabbit",
				},
			},
			Spec: k8sapi.PodSpec{
				NodeName: "nodeA",
			},
		}

		wep, err := wepConverter.Convert(&pod)
		It("should not generate a conversion error", func() {
			Expect(err).NotTo(HaveOccurred())
		})

		// Assert workloadID.
		It("should return workloadendpoint with correct workloadID", func() {
			Expect(wep.(api.WorkloadEndpoint).Metadata.Workload).To(Equal("default.podA"))
		})

		// Assert labels.
		var labels = map[string]string{
			"foo":           "bar",
			"roger":         "rabbit",
			"calico/k8s_ns": "default",
		}

		It("should return workloadendpoint with correct labels", func() {
			Expect(wep.(api.WorkloadEndpoint).Metadata.Labels).To(Equal(labels))
		})
	})
})
