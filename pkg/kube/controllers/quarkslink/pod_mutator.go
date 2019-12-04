package quarkslink

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"path/filepath"
	"regexp"

	"go.uber.org/zap"
	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/runtime/inject"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	"code.cloudfoundry.org/cf-operator/pkg/bosh/manifest"
	"code.cloudfoundry.org/quarks-utils/pkg/config"
)

// PodMutator for mounting quark link secrets on entangled pods
type PodMutator struct {
	client  client.Client
	log     *zap.SugaredLogger
	config  *config.Config
	decoder *admission.Decoder
}

// Check that PodMutator implements the admission.Handler interface
var _ admission.Handler = &PodMutator{}

// NewPodMutator returns a new mutator to mount secrets on entangled pods
func NewPodMutator(log *zap.SugaredLogger, config *config.Config) admission.Handler {
	mutatorLog := log.Named("quarks-link-pod-mutator")
	mutatorLog.Info("Creating a Pod mutator for QuarksLink")

	return &PodMutator{
		log:    mutatorLog,
		config: config,
	}
}

// Handle checks if the pod is an entangled pod and mounts the quarkslink secret, returns
// the unmodified pod otherwise
func (m *PodMutator) Handle(ctx context.Context, req admission.Request) admission.Response {
	pod := &corev1.Pod{}
	err := m.decoder.Decode(req, pod)
	if err != nil {
		return admission.Errored(http.StatusBadRequest, err)
	}
	m.log.Debugf("Pod mutator handler ran for pod '%s'", pod.Name)

	updatedPod := pod.DeepCopy()
	if validEntanglement(pod.GetAnnotations()) {
		err = m.addSecret(ctx, req.Namespace, updatedPod)
		if err != nil {
			return admission.Errored(http.StatusInternalServerError, err)
		}
	}

	marshaledPod, err := json.Marshal(updatedPod)
	if err != nil {
		return admission.Errored(http.StatusInternalServerError, err)
	}

	return admission.PatchResponseFromRaw(req.Object.Raw, marshaledPod)
}

func (m *PodMutator) addSecret(ctx context.Context, namespace string, pod *corev1.Pod) error {
	e := newEntanglement(pod.GetAnnotations())
	secretName, err := m.findSecret(ctx, namespace, e)
	if err != nil {
		m.log.Errorf("Couldn't list entanglement secrets for '%s/%s' in %s", e.deployment, e.consumes, namespace)
		return err
	}

	if secretName == "" {
		return fmt.Errorf("couldn't find entanglement secret '%s' for deployment '%s' in %s", e.consumes, e.deployment, namespace)
	}

	// add volume source to pod
	if !hasSecretVolumeSource(pod.Spec.Volumes, secretName) {
		volume := corev1.Volume{
			Name: secretName,
			VolumeSource: corev1.VolumeSource{
				Secret: &corev1.SecretVolumeSource{
					SecretName: secretName,
					Items: []corev1.KeyToPath{
						corev1.KeyToPath{
							Key:  e.consumes,
							Path: filepath.Join(e.deployment, "link.yaml"),
						},
					},
				},
			},
		}
		pod.Spec.Volumes = append(pod.Spec.Volumes, volume)
	}

	// create/update volume mount on containers
	mount := corev1.VolumeMount{
		Name:      secretName,
		ReadOnly:  true,
		MountPath: "/quarks/link",
	}
	for i, container := range pod.Spec.Containers {
		idx := findVolumeMount(container.VolumeMounts, secretName)
		if idx > -1 {
			container.VolumeMounts[idx] = mount
		} else {
			container.VolumeMounts = append(container.VolumeMounts, mount)
		}
		pod.Spec.Containers[i] = container
	}

	return nil
}

func (m *PodMutator) findSecret(ctx context.Context, namespace string, e entanglement) (string, error) {
	list := &corev1.SecretList{}
	// can't use entanglement labels, because quarks-job does not set
	// labels per container, so we list all secrets from the deployment
	labels := map[string]string{manifest.LabelDeploymentName: e.deployment}
	err := m.client.List(ctx, list, client.InNamespace(namespace), client.MatchingLabels(labels))
	if err != nil {
		return "", err
	}

	if len(list.Items) == 0 {
		return "", nil
	}

	// we can't use the instance group from
	// link-<deployment>-<instancegroup> for the search, because we don't
	// know which ig provides the link, so filter for secrets which match
	// the link name scheme
	var regex = regexp.MustCompile(fmt.Sprintf("^link-%s-[a-z0-9-]*$", e.deployment))
	for _, secret := range list.Items {
		if regex.MatchString(secret.Name) {
			// found an entanglement secret for the deployment,
			// does it have our link 'type.name'?
			if _, found := secret.Data[e.consumes]; found {
				return secret.Name, nil
			}
		}
	}

	return "", nil
}

func hasSecretVolumeSource(volumes []corev1.Volume, name string) bool {
	for _, v := range volumes {
		if v.Secret.SecretName == name {
			return true
		}
	}
	return false
}

func findVolumeMount(mounts []corev1.VolumeMount, name string) int {
	for i, m := range mounts {
		if m.Name == name {
			return i
		}
	}
	return -1
}

// Check that PodMutator implements the inject.Client interface
var _ inject.Client = &PodMutator{}

// InjectClient injects the client.
func (m *PodMutator) InjectClient(c client.Client) error {
	m.client = c
	return nil
}

// Check that PodMutator implements the admission.DecoderInjector interface
var _ admission.DecoderInjector = &PodMutator{}

// InjectDecoder injects the decoder.
func (m *PodMutator) InjectDecoder(d *admission.Decoder) error {
	m.decoder = d
	return nil
}