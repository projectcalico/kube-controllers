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

package loaders

import (
	apiv3 "github.com/projectcalico/libcalico-go/lib/apis/v3"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type NodeLoaderClient interface {
	Get(string, metav1.GetOptions) (*corev1.Node, error)
	List(metav1.ListOptions) (*corev1.NodeList, error)
}

type NodeLoader interface {
	Get(*apiv3.Node) (*corev1.Node, error)
	List() ([]corev1.Node, error)
}

type Node struct {
	k8sNodeClient NodeLoaderClient
	listOptions   metav1.ListOptions
	nodes         []corev1.Node
	fetched       bool
}

func (nl *Node) Get(calicoNode *apiv3.Node) (*corev1.Node, error) {
	if nl.fetched {
		if node := nl.findNode(calicoNode); node != nil {
			return node, nil
		}
	}

	kubeNode, err := nl.k8sNodeClient.Get(calicoNode.GetName(), metav1.GetOptions{})
	if err != nil {
		return nil, err
	}

	if !nl.fetched {
		nl.nodes = append(nl.nodes, *kubeNode)
	}

	return kubeNode, nil
}

func (nl *Node) List() ([]corev1.Node, error) {
	if nl.fetched {
		return nl.nodes, nil
	}

	nodeList, err := nl.k8sNodeClient.List(nl.listOptions)
	if err != nil {
		return nil, err
	}

	nl.fetched = true
	nl.nodes = nodeList.Items

	return nodeList.Items, nil
}

func (nl *Node) findNode(calicoNode *apiv3.Node) *corev1.Node {
	for _, node := range nl.nodes {
		if calicoNode.GetUID() == node.GetUID() {
			return &node
		}
	}

	return nil
}

func NewNodeLoader(k8sNodeClient NodeLoaderClient, listOptions metav1.ListOptions) NodeLoader {
	return &Node{
		k8sNodeClient: k8sNodeClient,
		listOptions:   listOptions,
	}
}
