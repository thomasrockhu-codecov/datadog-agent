// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016-present Datadog, Inc.

package config

import (
	"os"
	"path"
	"runtime"
	"strings"
	"time"

	"github.com/DataDog/datadog-agent/pkg/util/log"
	"github.com/DataDog/datadog-agent/pkg/util/system"
)

// Remember to also register feature in init()
const (
	// Docker socket present
	Docker Feature = "docker"
	// Containerd socket present
	Containerd Feature = "containerd"
	// Cri is any cri socket present
	Cri Feature = "cri"
	// Kubernetes environment
	Kubernetes Feature = "kubernetes"
	// ECSFargate environment
	ECSFargate Feature = "ecsfargate"
	// EKSFargate environment
	EKSFargate Feature = "eksfargate"
	// KubeOrchestratorExplorer can be enabled
	KubeOrchestratorExplorer Feature = "orchestratorexplorer"
	// CloudFoundry socket present
	CloudFoundry Feature = "cloudfoundry"
	// Podman containers storage path accessible
	Podman Feature = "podman"

	defaultLinuxDockerSocket           = "/var/run/docker.sock"
	defaultWindowsDockerSocketPath     = "//./pipe/docker_engine"
	defaultLinuxContainerdSocket       = "/var/run/containerd/containerd.sock"
	defaultWindowsContainerdSocketPath = "//./pipe/containerd-containerd"
	defaultLinuxCrioSocket             = "/var/run/crio/crio.sock"
	defaultHostMountPrefix             = "/host"
	defaultPodmanContainersStoragePath = "/var/lib/containers"
	unixSocketPrefix                   = "unix://"
	winNamedPipePrefix                 = "npipe://"

	socketTimeout = 500 * time.Millisecond
)

func init() {
	registerFeature(Docker)
	registerFeature(Containerd)
	registerFeature(Cri)
	registerFeature(Kubernetes)
	registerFeature(ECSFargate)
	registerFeature(EKSFargate)
	registerFeature(KubeOrchestratorExplorer)
	registerFeature(CloudFoundry)
	registerFeature(Podman)
}

func detectContainerFeatures(features FeatureMap) {
	detectKubernetes(features)
	detectDocker(features)
	detectContainerd(features)
	detectFargate(features)
	detectCloudFoundry(features)
	detectPodman(features)
}

func detectKubernetes(features FeatureMap) {
	if IsKubernetes() {
		features[Kubernetes] = struct{}{}
		if Datadog.GetBool("orchestrator_explorer.enabled") {
			features[KubeOrchestratorExplorer] = struct{}{}
		}
	}
}

func detectDocker(features FeatureMap) {
	if _, dockerHostSet := os.LookupEnv("DOCKER_HOST"); dockerHostSet {
		features[Docker] = struct{}{}
	} else {
		for _, defaultDockerSocketPath := range getDefaultDockerPaths() {
			exists, reachable := system.CheckSocketAvailable(defaultDockerSocketPath, socketTimeout)
			if exists && !reachable {
				log.Infof("Agent found Docker socket at: %s but socket not reachable (permissions?)", defaultDockerSocketPath)
				continue
			}

			if exists && reachable {
				features[Docker] = struct{}{}

				// Even though it does not modify configuration, using the OverrideFunc mechanism for uniformity
				AddOverrideFunc(func(Config) {
					os.Setenv("DOCKER_HOST", getDefaultDockerSocketType()+defaultDockerSocketPath)
				})
				break
			}
		}
	}
}

func detectContainerd(features FeatureMap) {
	// CRI Socket - Do not automatically default socket path if the Agent runs in Docker
	// as we'll very likely discover the containerd instance wrapped by Docker.
	criSocket := Datadog.GetString("cri_socket_path")
	if criSocket == "" && !IsDockerRuntime() {
		for _, defaultCriPath := range getDefaultCriPaths() {
			exists, reachable := system.CheckSocketAvailable(defaultCriPath, socketTimeout)
			if exists && !reachable {
				log.Infof("Agent found cri socket at: %s but socket not reachable (permissions?)", defaultCriPath)
				continue
			}

			if exists && reachable {
				criSocket = defaultCriPath
				AddOverride("cri_socket_path", defaultCriPath)
				// Currently we do not support multiple CRI paths
				break
			}
		}
	}

	if criSocket != "" {
		if isCriSupported() {
			features[Cri] = struct{}{}
		}

		if strings.Contains(criSocket, "containerd") {
			features[Containerd] = struct{}{}
		}
	}

	// Merge containerd_namespace with containerd_namespaces
	namespaces := merge(Datadog.GetStringSlice("containerd_namespaces"), Datadog.GetStringSlice("containerd_namespace"))
	AddOverride("containerd_namespace", namespaces)
	AddOverride("containerd_namespaces", namespaces)
}

func isCriSupported() bool {
	// Containerd support was historically meant for K8S
	// However, containerd is now used standalone elsewhere.
	return IsKubernetes()
}

func detectFargate(features FeatureMap) {
	isECSFargate := IsECSFargate()
	if isECSFargate {
		features[ECSFargate] = struct{}{}

		return
	}

	isEKSFargate := Datadog.GetBool("eks_fargate")
	if isEKSFargate {
		features[EKSFargate] = struct{}{}
		features[Kubernetes] = struct{}{}
	}

	if isECSFargate || isEKSFargate {
		// in AWS Fargate environments, dd_tags can't be attached to host.
		// The agent already add all tags present to dd_tags to each tagger entities to have them on metrics checks,
		// traces, events...
		// To ease customer configuration, the agent should inject dd_tags to the dogstatsd_tags configuration option
		// to have the same behaviour with dogstatsd metrics than with other metrics.
		dsdTags := merge(Datadog.GetStringSlice("dogstatsd_tags"), Datadog.GetStringSlice("dd_tags"))
		AddOverride("dogstatsd_tags", dsdTags)
	}
}

func detectCloudFoundry(features FeatureMap) {
	if Datadog.GetBool("cloud_foundry") {
		features[CloudFoundry] = struct{}{}
	}
}

func detectPodman(features FeatureMap) {
	for _, defaultPath := range getDefaultPodmanPaths() {
		if _, err := os.Stat(defaultPath); err == nil {
			features[Podman] = struct{}{}
			return
		}
	}
}

func getHostMountPrefixes() []string {
	if IsContainerized() {
		return []string{"", defaultHostMountPrefix}
	}
	return []string{""}
}

func getDefaultDockerSocketType() string {
	if runtime.GOOS == "windows" {
		return winNamedPipePrefix
	}

	return unixSocketPrefix
}

func getDefaultDockerPaths() []string {
	if runtime.GOOS == "windows" {
		return []string{defaultWindowsDockerSocketPath}
	}

	paths := []string{}
	for _, prefix := range getHostMountPrefixes() {
		paths = append(paths, path.Join(prefix, defaultLinuxDockerSocket))
	}
	return paths
}

func getDefaultCriPaths() []string {
	if runtime.GOOS == "windows" {
		return []string{defaultWindowsContainerdSocketPath}
	}

	paths := []string{}
	for _, prefix := range getHostMountPrefixes() {
		paths = append(paths, path.Join(prefix, defaultLinuxContainerdSocket), path.Join(prefix, defaultLinuxCrioSocket))
	}
	return paths
}

func getDefaultPodmanPaths() []string {
	paths := []string{}
	for _, prefix := range getHostMountPrefixes() {
		paths = append(paths, path.Join(prefix, defaultPodmanContainersStoragePath))
	}
	return paths
}

// merge merges and dedupes 2 slices without changing order
func merge(s1, s2 []string) []string {
	dedupe := map[string]struct{}{}
	merged := []string{}

	for _, elem := range append(s1, s2...) {
		if _, seen := dedupe[elem]; !seen {
			merged = append(merged, elem)
		}

		dedupe[elem] = struct{}{}
	}

	return merged
}
