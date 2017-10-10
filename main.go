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

package main

import (
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/projectcalico/k8s-policy/pkg/config"
	"github.com/projectcalico/k8s-policy/pkg/controllers/namespace"
	"github.com/projectcalico/k8s-policy/pkg/controllers/networkpolicy"
	"github.com/projectcalico/k8s-policy/pkg/controllers/pod"
	k8s "github.com/projectcalico/libcalico-go/lib/backend/k8s"
	"github.com/projectcalico/libcalico-go/lib/client"
	"github.com/projectcalico/libcalico-go/lib/logutils"
	log "github.com/sirupsen/logrus"
	"k8s.io/client-go/kubernetes"
	_ "k8s.io/client-go/plugin/pkg/client/auth"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"github.com/projectcalico/k8s-policy/pkg/controllers/node"
)

// VERSION is filled out during the build process (using git describe output)
var VERSION string

func main() {
	// Configure log formatting.
	log.SetFormatter(&logutils.Formatter{})

	// Install a hook that adds file/line no information.
	log.AddHook(&logutils.ContextHook{})

	// If `-v` is passed, display the version and exit.
	// Use a new flag set so as not to conflict with existing libraries which use "flag"
	flagSet := flag.NewFlagSet("Calico", flag.ExitOnError)
	version := flagSet.Bool("v", false, "Display version")
	err := flagSet.Parse(os.Args[1:])
	if err != nil {
		log.WithError(err).Fatal("Failed to parse flags")
	}
	if *version {
		fmt.Println(VERSION)
		os.Exit(0)
	}

	// Attempt to load configuration.
	config := new(config.Config)
	if err = config.Parse(); err != nil {
		log.WithError(err).Fatal("Failed to parse config")
	}
	log.WithField("config", config).Info("Loaded configuration from environment")

	// Set the log level based on the loaded configuration.
	logLevel, err := log.ParseLevel(config.LogLevel)
	if err != nil {
		logLevel = log.InfoLevel
	}
	log.SetLevel(logLevel)

	// Build clients to be used by the controllers.
	k8sClientset, calicoClient, extensionsClient, err := getClients(config.Kubeconfig)
	if err != nil {
		log.WithError(err).Fatal("Failed to start")
	}

	stop := make(chan struct{})
	defer close(stop)

	for _, controllerType := range strings.Split(config.EnabledControllers, ",") {
		switch controllerType {
		case "workloadendpoint":
			podController := pod.NewPodController(k8sClientset, calicoClient)
			go podController.Run(config.WorkloadEndpointWorkers, config.ReconcilerPeriod, stop)
		case "profile":
			namespaceController := namespace.NewNamespaceController(k8sClientset, calicoClient)
			go namespaceController.Run(config.ProfileWorkers, config.ReconcilerPeriod, stop)
		case "policy":
			policyController := networkpolicy.NewPolicyController(extensionsClient, calicoClient)
			go policyController.Run(config.PolicyWorkers, config.ReconcilerPeriod, stop)
		case "node":
			nodeController := node.NewNodeController(k8sClientset, calicoClient)
			go nodeController.Run(config.NodeWorkers, config.ReconcilerPeriod, stop)
		default:
			log.Fatalf("Invalid controller '%s' provided. Valid options are workloadendpoint, profile, policy", controllerType)
		}
	}

	// Wait forever.
	select {}
}

// getClients builds and returns Kubernetes, Calico and Extensions clients.
func getClients(kubeconfig string) (*kubernetes.Clientset, *client.Client, *rest.RESTClient, error) {
	// First, build the Calico client using the configured environment variables.
	cconfig, err := client.LoadClientConfig("")
	if err != nil {
		return nil, nil, nil, err
	}

	// Get Calico client
	calicoClient, err := client.New(*cconfig)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("failed to build Calico client: %s", err)
	}

	// Now build the Kubernetes client, we support in-cluster config and kubeconfig
	// as means of configuring the client.
	k8sconfig, err := clientcmd.BuildConfigFromFlags("", kubeconfig)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("failed to build kubernetes client config: %s", err)
	}

	// Get kubenetes clientset
	k8sClientset, err := kubernetes.NewForConfig(k8sconfig)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("failed to build kubernetes client: %s", err)
	}

	// Get extensions client
	extensionsClient, err := k8s.BuildExtensionsClientV1(*k8sconfig)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("failed to build extensions client: %s", err)
	}

	return k8sClientset, calicoClient, extensionsClient, nil
}
