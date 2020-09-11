// Copyright (c) 2020 IBM Corporation All rights reserved.
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

package routereflector

import (
	corev1 "k8s.io/api/core/v1"
)

func isNodeReady(node *corev1.Node) bool {
	for _, c := range node.Status.Conditions {
		if c.Type == corev1.NodeReady {
			return c.Status == "True"
		}
	}

	return false
}

func isNodeSchedulable(node *corev1.Node) bool {
	if node.Spec.Unschedulable == true {
		return false
	}
	for _, taint := range node.Spec.Taints {
		if _, ok := notReadyTaints[taint.Key]; ok {
			return false
		}
	}

	return true
}

func isNodeCompatible(node *corev1.Node, antyAfiinity map[string]*string) bool {
	for k, v := range node.GetLabels() {
		if iv, ok := antyAfiinity[k]; ok && (iv == nil || *iv == v) {
			return false
		}
	}

	return true
}
