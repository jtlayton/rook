/*
Copyright 2016 The Rook Authors. All rights reserved.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

	http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

// Package nfs for NFS ganesha
package nfs

import (
	"fmt"

	cephv1beta1 "github.com/rook/rook/pkg/apis/ceph.rook.io/v1beta1"
	"github.com/rook/rook/pkg/clusterd"
	opmon "github.com/rook/rook/pkg/operator/ceph/cluster/mon"
	"github.com/rook/rook/pkg/operator/k8sutil"
	"k8s.io/api/core/v1"
	extensions "k8s.io/api/extensions/v1beta1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
)

const (
	appName             = "rook-ceph-ganesha"
	ganeshaConfigVolume = "ganesha-config"
	ganeshaPort         = 2049
)

// Create the ganesha server
func (c *GaneshaController) createGanesha(n cephv1beta1.NFSGanesha) error {
	if err := validateGanesha(c.context, n); err != nil {
		return err
	}

	logger.Infof("start running ganesha %s", n.Name)

	for i := 0; i < n.Spec.Server.Active; i++ {
		name := k8sutil.IndexToName(i)

		configName, err := c.generateConfig(n, name)
		if err != nil {
			return fmt.Errorf("failed to create config. %+v", err)
		}

		// start the deployment
		deployment := c.makeDeployment(n, name, configName)
		_, err = c.context.Clientset.ExtensionsV1beta1().Deployments(n.Namespace).Create(deployment)
		if err != nil {
			if !errors.IsAlreadyExists(err) {
				return fmt.Errorf("failed to create mds deployment. %+v", err)
			}
			logger.Infof("ganesha deployment %s already exists", deployment.Name)
		} else {
			logger.Infof("ganesha deployment %s started", deployment.Name)
		}

		// create a service
		err = c.createGaneshaService(n, name)
		if err != nil {
			return fmt.Errorf("failed to create ganesha service. %+v", err)
		}

		if err = c.addServerToDatabase(n, name); err != nil {
			logger.Warningf("Failed to add ganesha server %s to database. It may already be added. %+v", name, err)
		}
	}

	return nil
}

func (c *GaneshaController) addServerToDatabase(n cephv1beta1.NFSGanesha, name string) error {
	logger.Infof("Adding ganesha %s to grace db", name)
	return c.context.Executor.ExecuteCommand(false, "", "ganesha-rados-grace", "--pool", n.Spec.ClientRecovery.Pool, "--ns", n.Spec.ClientRecovery.Namespace, "add", name)
}

func (c *GaneshaController) removeServerFromDatabase(n cephv1beta1.NFSGanesha, name string) error {
	logger.Infof("Removing ganesha %s from grace db", name)
	return c.context.Executor.ExecuteCommand(false, "", "ganesha-rados-grace", "--pool", n.Spec.ClientRecovery.Pool, "--ns", n.Spec.ClientRecovery.Namespace, "remove", name)
}

func (c *GaneshaController) generateConfig(n cephv1beta1.NFSGanesha, name string) (string, error) {

	data := map[string]string{
		"config": getGaneshaConfig(n.Spec, name),
	}
	configMap := &v1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      fmt.Sprintf("%s-%s-%s", appName, n.Name, name),
			Namespace: n.Namespace,
			Labels:    getLabels(n, name),
		},
		Data: data,
	}
	if _, err := c.context.Clientset.CoreV1().ConfigMaps(n.Namespace).Create(configMap); err != nil {
		if errors.IsAlreadyExists(err) {
			if _, err := c.context.Clientset.CoreV1().ConfigMaps(n.Namespace).Update(configMap); err != nil {
				return "", fmt.Errorf("failed to update ganesha config. %+v", err)
			}
			return configMap.Name, nil
		}
		return "", fmt.Errorf("failed to create ganesha config. %+v", err)
	}
	return configMap.Name, nil
}

func (c *GaneshaController) createGaneshaService(n cephv1beta1.NFSGanesha, name string) error {
	labels := getLabels(n, name)
	svc := &v1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:            instanceName(n, name),
			Namespace:       n.Namespace,
			Labels:          labels,
			OwnerReferences: []metav1.OwnerReference{c.ownerRef},
		},
		Spec: v1.ServiceSpec{
			Selector: labels,
			Ports: []v1.ServicePort{
				{
					Name:       "nfs",
					Port:       ganeshaPort,
					TargetPort: intstr.FromInt(int(ganeshaPort)),
					Protocol:   v1.ProtocolTCP,
				},
			},
		},
	}
	if c.hostNetwork {
		svc.Spec.ClusterIP = v1.ClusterIPNone
	}

	svc, err := c.context.Clientset.CoreV1().Services(n.Namespace).Create(svc)
	if err != nil {
		if !errors.IsAlreadyExists(err) {
			return fmt.Errorf("failed to create ganesha service. %+v", err)
		}
		logger.Infof("ganesha service already created")
		return nil
	}

	logger.Infof("ganesha service running at %s:%d", svc.Spec.ClusterIP, ganeshaPort)
	return nil
}

// Delete the ganesha server
func (c *GaneshaController) deleteGanesha(n cephv1beta1.NFSGanesha) error {
	for i := 0; i < n.Spec.Server.Active; i++ {
		name := k8sutil.IndexToName(i)

		// Remove from grace db
		if err := c.removeServerFromDatabase(n, name); err != nil {
			logger.Warningf("failed to remove server %s from grace db. %+v", name, err)
		}

		// Delete the mds deployment
		k8sutil.DeleteDeployment(c.context.Clientset, n.Namespace, instanceName(n, name))

		// Delete the ganesha service
		options := &metav1.DeleteOptions{}
		err := c.context.Clientset.CoreV1().Services(n.Namespace).Delete(instanceName(n, name), options)
		if err != nil && !errors.IsNotFound(err) {
			logger.Warningf("failed to delete ganesha service. %+v", err)
		}
	}

	return nil
}

func instanceName(n cephv1beta1.NFSGanesha, name string) string {
	return fmt.Sprintf("%s-%s-%s", appName, n.Name, name)
}

func (c *GaneshaController) makeDeployment(n cephv1beta1.NFSGanesha, name, configName string) *extensions.Deployment {
	deployment := &extensions.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:            instanceName(n, name),
			Namespace:       n.Namespace,
			OwnerReferences: []metav1.OwnerReference{c.ownerRef},
		},
	}
	configMapSource := &v1.ConfigMapVolumeSource{
		LocalObjectReference: v1.LocalObjectReference{Name: configName},
		Items:                []v1.KeyToPath{{Key: "config", Path: "ganesha.conf"}},
	}

	podSpec := v1.PodSpec{
		Containers:    []v1.Container{c.ganeshaContainer(n, name)},
		RestartPolicy: v1.RestartPolicyAlways,
		Volumes: []v1.Volume{
			{Name: k8sutil.DataDirVolume, VolumeSource: v1.VolumeSource{EmptyDir: &v1.EmptyDirVolumeSource{}}},
			{Name: ganeshaConfigVolume, VolumeSource: v1.VolumeSource{ConfigMap: configMapSource}},
			k8sutil.ConfigOverrideVolume(),
		},
		HostNetwork: c.hostNetwork,
	}
	if c.hostNetwork {
		podSpec.DNSPolicy = v1.DNSClusterFirstWithHostNet
	}
	n.Spec.Server.Placement.ApplyToPodSpec(&podSpec)

	podTemplateSpec := v1.PodTemplateSpec{
		ObjectMeta: metav1.ObjectMeta{
			Name:        instanceName(n, name),
			Labels:      getLabels(n, name),
			Annotations: map[string]string{},
		},
		Spec: podSpec,
	}

	// Multiple replicas of the ganesha service would be handled by creating a service and a new deployment for each one, rather than increasing the pod count here
	replicas := int32(1)
	deployment.Spec = extensions.DeploymentSpec{Template: podTemplateSpec, Replicas: &replicas}
	return deployment
}

func (c *GaneshaController) ganeshaContainer(n cephv1beta1.NFSGanesha, name string) v1.Container {

	return v1.Container{
		Args: []string{
			"ceph",
			"ganesha",
		},
		Name:  "nfs-ganesha",
		Image: c.rookImage,
		VolumeMounts: []v1.VolumeMount{
			{Name: k8sutil.DataDirVolume, MountPath: k8sutil.DataDir},
			{Name: ganeshaConfigVolume, MountPath: "/etc/ganesha"},
			k8sutil.ConfigOverrideMount(),
		},
		Env: []v1.EnvVar{
			{Name: "ROOK_POD_NAME", ValueFrom: &v1.EnvVarSource{FieldRef: &v1.ObjectFieldSelector{FieldPath: "metadata.name"}}},
			{Name: "ROOK_GANESHA_NAME", Value: name},
			opmon.ClusterNameEnvVar(n.Namespace),
			opmon.EndpointEnvVar(),
			opmon.AdminSecretEnvVar(),
			k8sutil.PodIPEnvVar(k8sutil.PrivateIPEnvVar),
			k8sutil.PodIPEnvVar(k8sutil.PublicIPEnvVar),
			k8sutil.ConfigOverrideEnvVar(),
		},
		Resources: n.Spec.Server.Resources,
	}
}

func getLabels(n cephv1beta1.NFSGanesha, name string) map[string]string {
	return map[string]string{
		k8sutil.AppAttr:     appName,
		k8sutil.ClusterAttr: n.Namespace,
		"nfs_ganesha":       n.Name,
		"instance":          name,
	}
}

func validateGanesha(context *clusterd.Context, n cephv1beta1.NFSGanesha) error {
	// core properties
	if n.Name == "" {
		return fmt.Errorf("missing name")
	}
	if n.Namespace == "" {
		return fmt.Errorf("missing namespace")
	}

	// Store properties
	if n.Spec.Store.Name == "" {
		return fmt.Errorf("missing storeName")
	}
	if n.Spec.Store.Type != "file" && n.Spec.Store.Type != "object" {
		return fmt.Errorf("unrecognized store type: %s", n.Spec.Store.Type)
	}

	// Client recovery properties
	if n.Spec.ClientRecovery.Pool == "" {
		return fmt.Errorf("missing clientRecovery.pool")
	}
	if n.Spec.ClientRecovery.Namespace == "" {
		return fmt.Errorf("missing clientRecovery.namesapce")
	}

	// Export properties
	if len(n.Spec.Exports) == 0 {
		return fmt.Errorf("at least one export is required")
	}
	for i, export := range n.Spec.Exports {
		if export.Path == "" {
			return fmt.Errorf("missing path for export %d", i)
		}
		if export.PseudoPath == "" {
			return fmt.Errorf("missing pseudoPath for export %d", i)
		}
		if err := verifyExportExists(context, export); err != nil {
			return fmt.Errorf("invalid export path. %+v", err)
		}
	}

	// Ganesha server properties
	if n.Spec.Server.Active == 0 {
		return fmt.Errorf("at least one active server required")
	}

	return nil
}

func verifyExportExists(context *clusterd.Context, export cephv1beta1.GaneshaExportSpec) error {
	// TODO: Check if the file or object store exist with the path to export
	return nil
}
