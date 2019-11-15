//go:generate rice embed-go

package boshgotemplate

import (
	"io/ioutil"
	"os"
	"os/exec"
	"path/filepath"

	rice "github.com/GeertJohan/go.rice"
	"github.com/pkg/errors"
	yaml "gopkg.in/yaml.v2"
)

const (
	rbClassFileName               = "bosh_erb_renderer.rb"
	evaluationContextYAMLFileName = "evaluation_context.yaml"
	instanceInfoYAMLFileName      = "instance_info.yaml"
	requiredVersion               = ">=2.2.1"
)

var (
	// RubyBinary is the name of the ruby binary. Can be an absolute path.
	RubyBinary = "ruby"
	// RubyGemBinary is the name of the ruby gem binary. Can be an absolute path.
	RubyGemBinary = "gem"
)

// EvaluationContext is the context passed to the erb renderer
type EvaluationContext struct {
	Properties map[string]interface{} `yaml:"properties"`
}

// InstanceInfo represents instance group runtime information
type InstanceInfo struct {
	Address    string `yaml:"address"`
	AZ         string `yaml:"az"`
	Bootstrap  bool   `yaml:"bootstrap"`
	Deployment string `yaml:"deployment"`
	ID         string `yaml:"id"`
	Index      int    `yaml:"index"`
	IP         string `yaml:"ip"`
	Name       string `yaml:"name"`
}

// ERBRenderer represents a BOSH Job erb template renderer
type ERBRenderer struct {
	EvaluationContext *EvaluationContext
	InstanceInfo      *InstanceInfo
	JobSpecFilePath   string
}

// NewERBRenderer creates a new ERBRenderer with an EvaluationContext
func NewERBRenderer(evaluationContext *EvaluationContext, instanceInfo *InstanceInfo, jobSpecFilePath string) *ERBRenderer {
	return &ERBRenderer{
		EvaluationContext: evaluationContext,
		JobSpecFilePath:   jobSpecFilePath,
		InstanceInfo:      instanceInfo,
	}
}

// Render renders an erb file using an EvaluationContext
func (e *ERBRenderer) Render(inputFilePath, outputFilePath string) (returnErr error) {
	// Check that dependencies are available
	if err := checkRubyAvailable(); err != nil {
		return err
	}
	if err := checkBOSHTemplateGemAvailable(); err != nil {
		return err
	}

	// Create a temporary work directory
	tmpDir, err := ioutil.TempDir("", "bosh-erb-renderer")
	if err != nil {
		return errors.Wrap(err, "failed to create temporary directory in erb renderer")
	}
	defer func() {
		if err = os.RemoveAll(tmpDir); err != nil {
			returnErr = errors.Wrap(err, "failed to cleanup erb renderer temporary directory")
		}
	}()

	// Write the ruby class to a file
	rbClassFilePath := filepath.Join(tmpDir, rbClassFileName)
	templateEvaluationContextRb, err := rice.
		MustFindBox("rb").
		Bytes("template_evaluation_context.rb")
	if err != nil {
		return errors.Wrap(err, "failed to load ruby class")
	}
	err = ioutil.WriteFile(rbClassFilePath, templateEvaluationContextRb, 0600)
	if err != nil {
		return errors.Wrap(err, "failed to write the rendering ruby class file")
	}

	// Marshal the evaluation context
	evalContextBytes, err := yaml.Marshal(e.EvaluationContext)
	if err != nil {
		return errors.Wrap(err, "failed to marshal the evaluation context")
	}
	evaluationContextYAMLFilePath := filepath.Join(tmpDir, evaluationContextYAMLFileName)
	err = ioutil.WriteFile(evaluationContextYAMLFilePath, evalContextBytes, 0600)
	if err != nil {
		return errors.Wrap(err, "failed to write the evaluation context yaml file")
	}

	// Marshal instance information
	instanceInfoBytes, err := yaml.Marshal(e.InstanceInfo)
	if err != nil {
		return errors.Wrap(err, "failed to marshal instance runtime information")
	}
	instanceInfoYAMLFilePath := filepath.Join(tmpDir, instanceInfoYAMLFileName)
	err = ioutil.WriteFile(instanceInfoYAMLFilePath, instanceInfoBytes, 0600)
	if err != nil {
		return errors.Wrap(err, "failed to write instance runtime information yaml file")
	}

	// Run rendering
	err = run(rbClassFilePath, evaluationContextYAMLFilePath, e.JobSpecFilePath, instanceInfoYAMLFilePath, inputFilePath, outputFilePath)
	if err != nil {
		return errors.Wrap(err, "failed to render template")
	}

	return nil
}

func run(rubyClassFilePath, evaluationContextYAMLFilePath, jobSpecFilePath, instanceInfoYAMLFilePath, inputFilePath, outputFilePath string) error {
	cmd := exec.Command(RubyBinary, rubyClassFilePath, evaluationContextYAMLFilePath, jobSpecFilePath, instanceInfoYAMLFilePath, inputFilePath, outputFilePath)
	outputBytes, err := cmd.CombinedOutput()
	if err != nil {
		output := string(outputBytes)
		return errors.Wrapf(err, "rendering failed: %s", output)
	}
	return nil
}

func checkRubyAvailable() error {
	_, err := exec.LookPath(RubyBinary)
	if err != nil {
		return errors.Wrap(err, "rendering BOSH templates requires ruby, please install ruby and make sure it's in your PATH")
	}
	return nil
}

func checkBOSHTemplateGemAvailable() error {
	cmd := exec.Command(RubyGemBinary, "list", "-i", "bosh-template", "-v", requiredVersion)
	outputBytes, err := cmd.CombinedOutput()

	if err != nil {
		output := string(outputBytes)
		return errors.Wrapf(err, "rendering BOSH templates requires the bosh-template(%s) ruby gem, please install it: %s ", requiredVersion, output)
	}

	return nil
}
