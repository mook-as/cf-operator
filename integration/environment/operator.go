package environment

import (
	"bytes"
	"context"
	"fmt"
	"log"
	"os"
	"os/exec"
	"strconv"
	"time"

	"github.com/go-logr/zapr"
	"github.com/pkg/errors"

	crlog "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/manager"

	"code.cloudfoundry.org/cf-operator/pkg/bosh/converter"
	"code.cloudfoundry.org/cf-operator/pkg/kube/operator"
	"code.cloudfoundry.org/cf-operator/pkg/kube/util/ctxlog"
	helper "code.cloudfoundry.org/cf-operator/pkg/testhelper"
)

// SetupLoggerContext sets up the logger and puts it into a new context
func (e *Environment) SetupLoggerContext(prefix string) context.Context {
	loggerPath := helper.LogfilePath(fmt.Sprintf("%s-%d.log", prefix, e.ID))
	e.ObservedLogs, e.Log = helper.NewTestLoggerWithPath(loggerPath)
	crlog.SetLogger(zapr.NewLogger(e.Log.Desugar()))

	return ctxlog.NewParentContext(e.Log)
}

// StartOperator prepares and starts the cf-operator
func (e *Environment) StartOperator() error {
	err := e.setupCFOperator()
	if err != nil {
		return errors.Wrapf(err, "Setting up CF Operator failed.")
	}

	e.Stop = e.startOperator()
	err = helper.WaitForPort(
		"127.0.0.1",
		strconv.Itoa(int(e.Config.WebhookServerPort)),
		1*time.Minute)
	if err != nil {
		return errors.Wrapf(err, "Integration setup failed. Waiting for port %d failed.", e.Config.WebhookServerPort)
	}

	return nil
}

func (e *Environment) setupCFOperator() error {
	var err error
	whh, found := os.LookupEnv("CF_OPERATOR_WEBHOOK_SERVICE_HOST")
	if !found {
		return errors.Errorf("Please set CF_OPERATOR_WEBHOOK_SERVICE_HOST to the host/ip the operator runs on and try again")
	}
	e.Config.WebhookServerHost = whh

	port, err := getWebhookServicePort(e.ID)
	if err != nil {
		return err
	}

	log.Printf("DEBUG: Setting up webhook on %s:%d\n", whh, port)

	sshUser, shouldForwardPort := os.LookupEnv("ssh_server_user")
	if shouldForwardPort {
		var remoteAddr string
		var ok bool
		if remoteAddr, ok = os.LookupEnv("ssh_server_listen_address"); !ok {
			remoteAddr = whh
		}
		log.Printf("DEBUG: Port forward: localhost:%[1]d -> %s:%[1]d via %[3]s\n", port, remoteAddr, whh)
		cmd := exec.Command(
			"ssh", "-vvfNT", "-i", "/tmp/cf-operator-tunnel-identity", "-o",
			"UserKnownHostsFile=/dev/null", "-o", "StrictHostKeyChecking=no", "-R",
			fmt.Sprintf("%s:%[2]d:localhost:%[2]d", remoteAddr, port),
			fmt.Sprintf("%s@%s", sshUser, whh))
		var output bytes.Buffer
		cmd.Stdout = &output
		cmd.Stderr = &output
		err = cmd.Start()
		if err != nil {
			return err
		}
		go func() {
			err := cmd.Wait()
			if err != nil {
				fmt.Printf("SSH TUNNEL FAILED: %w\nOUTPUT: %s\n", err, output.String())
			} else {
				fmt.Printf("SSH TUNNEL (port %d) CLOSED:\n%s\n", port, output.String())
			}
		}()
	}

	e.Config.WebhookServerPort = port

	e.Config.OperatorNamespace = e.Namespace
	e.Config.Namespace = e.Namespace

	dockerImageOrg, found := os.LookupEnv("DOCKER_IMAGE_ORG")
	if !found {
		dockerImageOrg = "cfcontainerization"
	}

	dockerImageRepo, found := os.LookupEnv("DOCKER_IMAGE_REPOSITORY")
	if !found {
		dockerImageRepo = "cf-operator"
	}

	dockerImageTag, found := os.LookupEnv("DOCKER_IMAGE_TAG")
	if !found {
		return errors.Errorf("required environment variable DOCKER_IMAGE_TAG not set")
	}

	err = converter.SetupOperatorDockerImage(dockerImageOrg, dockerImageRepo, dockerImageTag)
	if err != nil {
		return err
	}

	ctx := e.SetupLoggerContext("cf-operator-tests")

	e.mgr, err = operator.NewManager(ctx, e.Config, e.KubeConfig, manager.Options{
		Namespace:          e.Namespace,
		MetricsBindAddress: "0",
		LeaderElection:     false,
		Port:               int(e.Config.WebhookServerPort),
		Host:               "0.0.0.0",
	})

	return err
}

func (e *Environment) startOperator() chan struct{} {
	stop := make(chan struct{})
	go func() {
		err := e.mgr.Start(stop)
		if err != nil {
			panic(err)
		}
	}()
	return stop
}

func getWebhookServicePort(namespaceCounter int) (int32, error) {
	port := int64(40000)
	portString, found := os.LookupEnv("CF_OPERATOR_WEBHOOK_SERVICE_PORT")
	if found {
		var err error
		port, err = strconv.ParseInt(portString, 10, 32)
		if err != nil {
			return -1, errors.Wrapf(err, "Parsing portSting %s failed", portString)
		}
	}
	return int32(port) + int32(namespaceCounter), nil
}
