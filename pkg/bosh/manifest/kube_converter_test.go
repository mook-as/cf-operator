package manifest_test

import (
	"fmt"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	"github.com/onsi/gomega/format"
	corev1 "k8s.io/api/core/v1"

	"code.cloudfoundry.org/cf-operator/pkg/bosh/bpm"
	"code.cloudfoundry.org/cf-operator/pkg/bosh/manifest"
	esv1 "code.cloudfoundry.org/cf-operator/pkg/kube/apis/extendedsecret/v1alpha1"
	essv1 "code.cloudfoundry.org/cf-operator/pkg/kube/apis/extendedstatefulset/v1alpha1"
	"code.cloudfoundry.org/cf-operator/testing"
	"code.cloudfoundry.org/cf-operator/testing/boshreleases"
)

var _ = Describe("kube converter", func() {
	var (
		m   manifest.Manifest
		env testing.Catalog
	)

	Describe("Variables", func() {
		BeforeEach(func() {
			m = env.DefaultBOSHManifest()
			format.TruncatedDiff = false
		})

		act := func() []esv1.ExtendedSecret {
			kubeConfig := manifest.NewKubeConfig("foo", m.Name, &m)
			return kubeConfig.Variables(m.Variables)
		}

		Context("converting variables", func() {
			It("sanitizes secret names", func() {
				m.Name = "-abc_123.?!\"§$&/()=?"
				m.Variables[0].Name = "def-456.?!\"§$&/()=?-"

				variables := act()
				Expect(variables[0].Name).To(Equal("abc-123.var-def-456"))
			})

			It("trims secret names to 63 characters", func() {
				m.Name = "foo"
				m.Variables[0].Name = "this-is-waaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaay-too-long"

				variables := act()
				Expect(variables[0].Name).To(Equal("foo.var-this-is-waaaaaaaaaaaaaa5bffdb0302ac051d11f52d2606254a5f"))
			})

			It("converts password variables", func() {
				variables := act()
				Expect(len(variables)).To(Equal(1))

				var1 := variables[0]
				Expect(var1.Name).To(Equal("foo-deployment.var-adminpass"))
				Expect(var1.Spec.Type).To(Equal(esv1.Password))
				Expect(var1.Spec.SecretName).To(Equal("foo-deployment.var-adminpass"))
			})

			It("converts rsa key variables", func() {
				m.Variables[0] = manifest.Variable{
					Name: "adminkey",
					Type: "rsa",
				}
				variables := act()
				Expect(variables).To(HaveLen(1))

				var1 := variables[0]
				Expect(var1.Name).To(Equal("foo-deployment.var-adminkey"))
				Expect(var1.Spec.Type).To(Equal(esv1.RSAKey))
				Expect(var1.Spec.SecretName).To(Equal("foo-deployment.var-adminkey"))
			})

			It("converts ssh key variables", func() {
				m.Variables[0] = manifest.Variable{
					Name: "adminkey",
					Type: "ssh",
				}
				variables := act()
				Expect(variables).To(HaveLen(1))

				var1 := variables[0]
				Expect(var1.Name).To(Equal("foo-deployment.var-adminkey"))
				Expect(var1.Spec.Type).To(Equal(esv1.SSHKey))
				Expect(var1.Spec.SecretName).To(Equal("foo-deployment.var-adminkey"))
			})

			It("converts certificate variables", func() {
				m.Variables[0] = manifest.Variable{
					Name: "foo-cert",
					Type: "certificate",
					Options: &manifest.VariableOptions{
						CommonName:       "example.com",
						AlternativeNames: []string{"foo.com", "bar.com"},
						IsCA:             true,
						CA:               "theca",
						ExtendedKeyUsage: []manifest.AuthType{manifest.ClientAuth},
					},
				}
				variables := act()
				Expect(variables).To(HaveLen(1))

				var1 := variables[0]
				Expect(var1.Name).To(Equal("foo-deployment.var-foo-cert"))
				Expect(var1.Spec.Type).To(Equal(esv1.Certificate))
				Expect(var1.Spec.SecretName).To(Equal("foo-deployment.var-foo-cert"))
				request := var1.Spec.Request.CertificateRequest
				Expect(request.CommonName).To(Equal("example.com"))
				Expect(request.AlternativeNames).To(Equal([]string{"foo.com", "bar.com"}))
				Expect(request.IsCA).To(Equal(true))
				Expect(request.CARef.Name).To(Equal("foo-deployment.var-theca"))
				Expect(request.CARef.Key).To(Equal("certificate"))
			})
		})

	})

	Context("ApplyBPMInfo", func() {
		act := func(bpmConfigs map[string]bpm.Configs) (*manifest.KubeConfig, error) {
			kubeConfig := manifest.NewKubeConfig("foo", m.Name, &m)
			err := kubeConfig.ApplyBPMInfo(m.InstanceGroups, bpmConfigs)
			return kubeConfig, err
		}

		BeforeEach(func() {
			m = env.DefaultBOSHManifest()
		})

		Context("when BPM is missing in configs", func() {
			It("returns an error", func() {
				bpmConfigs := map[string]bpm.Configs{}
				_, err := act(bpmConfigs)
				Expect(err).Should(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("couldn't find instance group 'redis-slave' in bpm configs set"))
			})
		})

		Context("when a BPM config is present", func() {
			var bpmConfigs map[string]bpm.Configs
			BeforeEach(func() {
				c, err := bpm.NewConfig([]byte(boshreleases.DefaultBPMConfig))
				Expect(err).ShouldNot(HaveOccurred())

				bpmConfigs = map[string]bpm.Configs{
					"redis-slave": bpm.Configs{"redis-server": c},
					"diego-cell":  bpm.Configs{"cflinuxfs3-rootfs-setup": c},
				}
			})

			Context("when the lifecycle is set to service", func() {
				It("converts the instance group to an ExtendedStatefulSet", func() {
					kubeConfig, err := act(bpmConfigs)
					Expect(err).ShouldNot(HaveOccurred())
					extStS := kubeConfig.InstanceGroups[0].Spec.Template.Spec.Template
					Expect(extStS.Name).To(Equal("diego-cell"))

					specCopierInitContainer := extStS.Spec.InitContainers[0]
					rendererInitContainer := extStS.Spec.InitContainers[1]

					// Test containers in the extended statefulSet
					Expect(extStS.Spec.Containers[0].Image).To(Equal("hub.docker.com/cfcontainerization/cflinuxfs3:opensuse-15.0-28.g837c5b3-30.263-7.0.0_233.gde0accd0-0.62.0"))
					Expect(extStS.Spec.Containers[0].Command).To(Equal([]string{"/var/vcap/packages/test-server/bin/test-server"}))
					Expect(extStS.Spec.Containers[0].Name).To(Equal("cflinuxfs3-rootfs-setup-test-server"))

					// Test init containers in the extended statefulSet
					Expect(specCopierInitContainer.Name).To(Equal("spec-copier-cflinuxfs3"))
					Expect(specCopierInitContainer.Image).To(Equal("hub.docker.com/cfcontainerization/cflinuxfs3:opensuse-15.0-28.g837c5b3-30.263-7.0.0_233.gde0accd0-0.62.0"))
					Expect(specCopierInitContainer.Command[0]).To(Equal("bash"))
					Expect(specCopierInitContainer.Name).To(Equal("spec-copier-cflinuxfs3"))
					Expect(rendererInitContainer.Image).To(Equal("/:"))
					Expect(rendererInitContainer.Name).To(Equal("renderer-diego-cell"))

					// Test shared volume setup
					Expect(extStS.Spec.Containers[0].VolumeMounts[0].Name).To(Equal("rendering-data"))
					Expect(extStS.Spec.Containers[0].VolumeMounts[0].MountPath).To(Equal("/var/vcap/all-releases"))
					Expect(specCopierInitContainer.VolumeMounts[0].Name).To(Equal("rendering-data"))
					Expect(specCopierInitContainer.VolumeMounts[0].MountPath).To(Equal("/var/vcap/all-releases"))
					Expect(rendererInitContainer.VolumeMounts[0].Name).To(Equal("rendering-data"))
					Expect(rendererInitContainer.VolumeMounts[0].MountPath).To(Equal("/var/vcap/all-releases"))

					// Test the renderer container setup
					Expect(rendererInitContainer.Env[0].Name).To(Equal("INSTANCE_GROUP_NAME"))
					Expect(rendererInitContainer.Env[0].Value).To(Equal("diego-cell"))
					Expect(rendererInitContainer.VolumeMounts[0].Name).To(Equal("rendering-data"))
					Expect(rendererInitContainer.VolumeMounts[0].MountPath).To(Equal("/var/vcap/all-releases"))
					Expect(rendererInitContainer.VolumeMounts[1].Name).To(Equal("jobs-dir"))
					Expect(rendererInitContainer.VolumeMounts[1].MountPath).To(Equal("/var/vcap/jobs"))
					Expect(rendererInitContainer.VolumeMounts[2].Name).To(Equal("ig-resolved"))
					Expect(rendererInitContainer.VolumeMounts[2].MountPath).To(Equal("/var/run/secrets/resolved-properties/diego-cell"))

					// Test services for the extended statefulSet
					service0 := kubeConfig.Services[0]
					Expect(service0.Name).To(Equal(fmt.Sprintf("%s-%s-0", m.Name, extStS.Name)))
					Expect(service0.Spec.Selector).To(Equal(map[string]string{
						manifest.LabelInstanceGroupName: extStS.Name,
						essv1.LabelAZIndex:              "0",
						essv1.LabelPodOrdinal:           "0",
					}))
					Expect(service0.Spec.Ports).To(Equal([]corev1.ServicePort{
						{
							Name:     "rep-server",
							Protocol: corev1.ProtocolTCP,
							Port:     1801,
						},
					}))

					service1 := kubeConfig.Services[1]
					Expect(service1.Name).To(Equal(fmt.Sprintf("%s-%s-1", m.Name, extStS.Name)))
					Expect(service1.Spec.Selector).To(Equal(map[string]string{
						manifest.LabelInstanceGroupName: extStS.Name,
						essv1.LabelAZIndex:              "1",
						essv1.LabelPodOrdinal:           "0",
					}))
					Expect(service1.Spec.Ports).To(Equal([]corev1.ServicePort{
						{
							Name:     "rep-server",
							Protocol: corev1.ProtocolTCP,
							Port:     1801,
						},
					}))

					service2 := kubeConfig.Services[2]
					Expect(service2.Name).To(Equal(fmt.Sprintf("%s-%s-2", m.Name, extStS.Name)))
					Expect(service2.Spec.Selector).To(Equal(map[string]string{
						manifest.LabelInstanceGroupName: extStS.Name,
						essv1.LabelAZIndex:              "0",
						essv1.LabelPodOrdinal:           "1",
					}))
					Expect(service2.Spec.Ports).To(Equal([]corev1.ServicePort{
						{
							Name:     "rep-server",
							Protocol: corev1.ProtocolTCP,
							Port:     1801,
						},
					}))

					service3 := kubeConfig.Services[3]
					Expect(service3.Name).To(Equal(fmt.Sprintf("%s-%s-3", m.Name, extStS.Name)))
					Expect(service3.Spec.Selector).To(Equal(map[string]string{
						manifest.LabelInstanceGroupName: extStS.Name,
						essv1.LabelAZIndex:              "1",
						essv1.LabelPodOrdinal:           "1",
					}))
					Expect(service3.Spec.Ports).To(Equal([]corev1.ServicePort{
						{
							Name:     "rep-server",
							Protocol: corev1.ProtocolTCP,
							Port:     1801,
						},
					}))

					headlessService := kubeConfig.Services[4]
					Expect(headlessService.Name).To(Equal(fmt.Sprintf("%s-%s", m.Name, extStS.Name)))
					Expect(headlessService.Spec.Selector).To(Equal(map[string]string{
						manifest.LabelInstanceGroupName: extStS.Name,
					}))
					Expect(headlessService.Spec.Ports).To(Equal([]corev1.ServicePort{
						{
							Name:     "rep-server",
							Protocol: corev1.ProtocolTCP,
							Port:     1801,
						},
					}))
					Expect(headlessService.Spec.ClusterIP).To(Equal("None"))
				})
			})

			Context("when the lifecycle is set to errand", func() {
				It("converts the instance group to an ExtendedJob", func() {
					kubeConfig, err := act(bpmConfigs)
					Expect(err).ShouldNot(HaveOccurred())
					Expect(kubeConfig.Errands).To(HaveLen(1))

					eJob := kubeConfig.Errands[0]
					Expect(eJob.Name).To(Equal("foo-deployment-redis-slave"))

					specCopierInitContainer := eJob.Spec.Template.Spec.InitContainers[0]
					rendererInitContainer := eJob.Spec.Template.Spec.InitContainers[1]

					// Test containers in the extended job
					Expect(eJob.Spec.Template.Spec.Containers[0].Name).To(Equal("redis-server-test-server"))
					Expect(eJob.Spec.Template.Spec.Containers[0].Image).To(Equal("hub.docker.com/cfcontainerization/redis:opensuse-42.3-28.g837c5b3-30.263-7.0.0_234.gcd7d1132-36.15.0"))
					Expect(eJob.Spec.Template.Spec.Containers[0].Command).To(Equal([]string{"/var/vcap/packages/test-server/bin/test-server"}))

					// Test init containers in the extended job
					Expect(specCopierInitContainer.Name).To(Equal("spec-copier-redis"))
					Expect(specCopierInitContainer.Image).To(Equal("hub.docker.com/cfcontainerization/redis:opensuse-42.3-28.g837c5b3-30.263-7.0.0_234.gcd7d1132-36.15.0"))
					Expect(specCopierInitContainer.Command[0]).To(Equal("bash"))
					Expect(rendererInitContainer.Image).To(Equal("/:"))
					Expect(rendererInitContainer.Name).To(Equal("renderer-redis-slave"))

					// Test shared volume setup
					Expect(eJob.Spec.Template.Spec.Containers[0].VolumeMounts[0].Name).To(Equal("rendering-data"))
					Expect(eJob.Spec.Template.Spec.Containers[0].VolumeMounts[0].MountPath).To(Equal("/var/vcap/all-releases"))
					Expect(specCopierInitContainer.VolumeMounts[0].Name).To(Equal("rendering-data"))
					Expect(specCopierInitContainer.VolumeMounts[0].MountPath).To(Equal("/var/vcap/all-releases"))
					Expect(rendererInitContainer.VolumeMounts[0].Name).To(Equal("rendering-data"))
					Expect(rendererInitContainer.VolumeMounts[0].MountPath).To(Equal("/var/vcap/all-releases"))

					// Test mounting the resolved instance group properties in the renderer container
					Expect(rendererInitContainer.Env[0].Name).To(Equal("INSTANCE_GROUP_NAME"))
					Expect(rendererInitContainer.Env[0].Value).To(Equal("redis-slave"))
					Expect(rendererInitContainer.VolumeMounts[1].Name).To(Equal("jobs-dir"))
					Expect(rendererInitContainer.VolumeMounts[1].MountPath).To(Equal("/var/vcap/jobs"))
				})
			})
		})

		Context("when multiple BPM processes exist", func() {
			var bpmConfigs map[string]bpm.Configs

			BeforeEach(func() {
				m = *env.BOSHManifestWithBPM()

				c1, err := bpm.NewConfig([]byte(boshreleases.DefaultBPMConfig))
				Expect(err).ShouldNot(HaveOccurred())
				c2, err := bpm.NewConfig([]byte(boshreleases.MultiProcessBPMConfig))
				Expect(err).ShouldNot(HaveOccurred())

				bpmConfigs = map[string]bpm.Configs{
					"fake-ig-1": bpm.Configs{
						"fake-errand-a": c1,
						"fake-errand-b": c2,
					},
					"fake-ig-2": bpm.Configs{
						"fake-job-a": c1,
						"fake-job-b": c1,
						"fake-job-c": c2,
					},
					"fake-ig-3": bpm.Configs{
						"fake-job-a": c1,
						"fake-job-b": c1,
						"fake-job-c": c2,
						"fake-job-d": c2,
					},
				}
			})

			It("creates a k8s container for each BPM process", func() {
				kubeConfig, err := act(bpmConfigs)
				Expect(err).ShouldNot(HaveOccurred())
				Expect(kubeConfig.InstanceGroups).To(HaveLen(2))
				Expect(kubeConfig.Errands).To(HaveLen(1))

				containers := kubeConfig.Errands[0].Spec.Template.Spec.Containers
				Expect(containers).To(HaveLen(3))
				Expect(containers[0].Name).To(Equal("fake-errand-a-test-server"))
				Expect(containers[0].Command[0]).To(ContainSubstring("bin/test-server"))
				Expect(containers[0].Args).To(HaveLen(2))
				Expect(containers[0].Args[0]).To(Equal("--port"))
				Expect(containers[0].Args[1]).To(Equal("1337"))
				Expect(containers[0].Env).To(HaveLen(1))
				Expect(containers[0].Env[0].Name).To(Equal("BPM"))
				Expect(containers[0].Env[0].Value).To(Equal("SWEET"))
				Expect(containers[1].Name).To(Equal("fake-errand-b-test-server"))
				Expect(containers[2].Name).To(Equal("fake-errand-b-alt-test-server"))
				Expect(containers[2].Command[0]).To(ContainSubstring("bin/test-server"))
				Expect(containers[2].Args).To(HaveLen(3))
				Expect(containers[2].Args[0]).To(Equal("--port"))
				Expect(containers[2].Args[1]).To(Equal("1338"))
				Expect(containers[2].Env).To(HaveLen(1))
				Expect(containers[2].Env[0].Name).To(Equal("BPM"))
				Expect(containers[2].Env[0].Value).To(Equal("CONTAINED"))

				containers = kubeConfig.InstanceGroups[0].Spec.Template.Spec.Template.Spec.Containers
				Expect(containers).To(HaveLen(4))
				Expect(containers[0].Name).To(Equal("fake-job-a-test-server"))
				Expect(containers[1].Name).To(Equal("fake-job-b-test-server"))
				Expect(containers[2].Name).To(Equal("fake-job-c-test-server"))
				Expect(containers[3].Name).To(Equal("fake-job-c-alt-test-server"))

				containers = kubeConfig.InstanceGroups[1].Spec.Template.Spec.Template.Spec.Containers
				Expect(containers).To(HaveLen(6))
				Expect(containers[0].Name).To(Equal("fake-job-a-test-server"))
				Expect(containers[1].Name).To(Equal("fake-job-b-test-server"))
				Expect(containers[2].Name).To(Equal("fake-job-c-test-server"))
				Expect(containers[3].Name).To(Equal("fake-job-c-alt-test-server"))
				Expect(containers[4].Name).To(Equal("fake-job-d-test-server"))
				Expect(containers[5].Name).To(Equal("fake-job-d-alt-test-server"))
			})
		})
	})
})
