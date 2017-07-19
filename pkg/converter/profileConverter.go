package converter

import (
	"github.com/projectcalico/libcalico-go/lib/api/unversioned"
	"github.com/projectcalico/libcalico-go/lib/api"
	"k8s.io/client-go/pkg/api/v1"

)
const NS_POLICY_ANNOTATION = "net.beta.kubernetes.io/network-policy"
// Format to use for namespace profile names.
const NS_PROFILE_FMT = "k8s_ns.%s"
type profileConverter struct{


}

func (p *profileConverter)convert(k8sObj interface{})(interface{},error){

	// Converting obj to calico representation
	labels := k8sObj.(*v1.Namespace).Labels
	profile := api.Profile{
		TypeMetadata: unversioned.TypeMetadata{
			Kind:  "profile",     
			APIVersion: "v1",
		}, 
		Metadata: api.ProfileMetadata{
			Name: k8sObj.(*v1.Namespace).Name,
			Labels: k8sObj.(*v1.Namespace).Labels
		},
	}

	return profile
}