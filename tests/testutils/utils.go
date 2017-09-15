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

// This file contains test utils that are general purpose
// and should one day be moved to a central location for use across all
// projects.

package testutils

import (
	"fmt"
	"os/exec"

	. "github.com/onsi/gomega"
	log "github.com/sirupsen/logrus"

	"github.com/projectcalico/felix/fv/containers"
	"github.com/projectcalico/libcalico-go/lib/api"
	"github.com/projectcalico/libcalico-go/lib/client"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
)

const vK8sApiserver = "v1.7.3"

func RunK8sApiserver(etcdIp string) *containers.Container {
	return containers.Run("st-apiserver",
		fmt.Sprintf("gcr.io/google_containers/hyperkube-amd64:%s", vK8sApiserver),
		"/hyperkube", "apiserver",
		"--service-cluster-ip-range=10.101.0.0/16",
		"--authorization-mode=AlwaysAllow",
		"--insecure-port=8080",
		"--insecure-bind-address=0.0.0.0",
		fmt.Sprintf("--etcd-servers=http://%s:2379", etcdIp),
	)
}

func RunEtcd() *containers.Container {
	return containers.Run("etcd-fv",
		"quay.io/coreos/etcd",
		"etcd",
		"--advertise-client-urls", "http://127.0.0.1:2379",
		"--listen-client-urls", "http://0.0.0.0:2379")
}

func GetCalicoClient(etcdIP string) *client.Client {
	client, err := client.New(api.CalicoAPIConfig{
		Spec: api.CalicoAPIConfigSpec{
			DatastoreType: api.EtcdV2,
			EtcdConfig: api.EtcdConfig{
				EtcdEndpoints: "http://" + etcdIP + ":2379",
			},
		},
	})
	Expect(err).NotTo(HaveOccurred())
	return client
}

// GetK8sClient gets a kubernetes client.
func GetK8sClient(kubeconfig string) (*kubernetes.Clientset, error) {
	k8sconfig, err := clientcmd.BuildConfigFromFlags("", kubeconfig)
	if err != nil {
		return nil, fmt.Errorf("failed to build kubernetes client config: %s", err)
	}

	k8sClientset, err := kubernetes.NewForConfig(k8sconfig)
	if err != nil {
		return nil, fmt.Errorf("failed to build kubernetes client: %s", err)
	}

	return k8sClientset, nil
}

func Exec(c *containers.Container, command []string) {
	runArgs := append([]string{"exec", c.Name}, command...)
	runCmd := exec.Command("docker", runArgs...)
	err := runCmd.Start()
	Expect(err).NotTo(HaveOccurred())
	outputBytes, err := runCmd.CombinedOutput()
	log.Infof("Etcd dump: %s", outputBytes)
}
