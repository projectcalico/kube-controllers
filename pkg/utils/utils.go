package utils

import (
	"flag"
	log "github.com/Sirupsen/logrus"
	"github.com/projectcalico/libcalico-go/lib/client"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
	_ "k8s.io/client-go/plugin/pkg/client/auth"
)

// Fuction that returns kubernetes and calico clients.
func GetClients() (*kubernetes.Clientset, *client.Client, error) {

	cconfig, err := client.LoadClientConfig("")
	if err != nil {
		return nil, nil, err
	}

	// Get Calico client
	calicoClient, err := client.New(*cconfig)
	if err != nil {
		panic(err)
	}

	var kubeconfig string
	var master string

	flag.StringVar(&kubeconfig, "kubeconfig", "", "absolute path to the kubeconfig file")
	flag.StringVar(&master, "master", "", "master url")
	flag.Parse()

	// creates the connection
	k8sconfig, err := clientcmd.BuildConfigFromFlags(master, kubeconfig)
	if err != nil {
		log.Fatal(err)
	}
	if err != nil {
		return nil, nil, err
	}

	// Get kubenetes clientset
	k8sClientset, err := kubernetes.NewForConfig(k8sconfig)
	if err != nil {
		panic(err.Error())
	}

	return k8sClientset, calicoClient, nil
}
