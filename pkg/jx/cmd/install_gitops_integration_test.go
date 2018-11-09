// +build integration

package cmd_test

import (
	"github.com/jenkins-x/jx/pkg/client/clientset/versioned"
	"github.com/jenkins-x/jx/pkg/gits"
	"github.com/jenkins-x/jx/pkg/helm"
	"github.com/jenkins-x/jx/pkg/kube"
	"github.com/stretchr/testify/require"
	"io/ioutil"
	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/helm/pkg/chartutil"
	"os"
	"path/filepath"
	"testing"

	"github.com/jenkins-x/jx/pkg/jx/cmd"
	"github.com/stretchr/testify/assert"
)

func TestInstallGitOps(t *testing.T) {
	t.Parallel()

	tempDir, err := ioutil.TempDir("", "test-install-gitops")
	assert.NoError(t, err)

	const clusterAdminRoleName = "cluster-admin"

	clusterAdminRole := &rbacv1.Role{
		ObjectMeta: metav1.ObjectMeta{
			Name: clusterAdminRoleName,
		},
		Rules: []rbacv1.PolicyRule{
			{
				Verbs:     []string{"get", "watch", "list", "create", "delete", "update", "patch"},
				APIGroups: []string{""},
				Resources: []string{"*"},
			},
		},
	}

	co := cmd.CommonOptions{
		In:  os.Stdin,
		Out: os.Stdout,
		Err: os.Stderr,
	}
	o := cmd.CreateInstallOptions(co.Factory, co.In, co.Out, co.Err)

	cmd.ConfigureTestOptionsWithResources(&o.CommonOptions,
		[]runtime.Object{
			clusterAdminRole,
		},
		[]runtime.Object{
		},
		gits.NewGitCLI(),
		helm.NewHelmCLI("helm", helm.V2, "", true),
	)

	o.InitOptions.CommonOptions = o.CommonOptions
	o.CreateEnvOptions.CommonOptions = o.CommonOptions

	jxClient, ns, err := o.JXClientAndDevNamespace()
	require.NoError(t, err, "failed to create JXClient")
	kubeClient, _, err := o.KubeClient()
	require.NoError(t, err, "failed to create KubeClient")

	// lets remove the default generated Environment so we can assert that we don't create any environments
	// via: jx import --gitops
	jxClient.JenkinsV1().Environments(ns).Delete(kube.LabelValueDevEnvironment, nil)
	assertNoEnvironments(t, jxClient, ns)

	o.Flags.Provider = cmd.GKE
	o.Flags.Dir = tempDir
	o.Flags.GitOpsMode = true
	// TODO fix
	o.Flags.NoDefaultEnvironments = true
	o.InitOptions.Flags.SkipTiller = true
	o.InitOptions.Flags.NoTiller = true
	o.InitOptions.Flags.SkipIngress = true
	o.InitOptions.Flags.UserClusterRole = clusterAdminRoleName
	o.BatchMode = true
	o.Headless = true

	err = o.Run()
	require.NoError(t, err, "Failed to run jx install")

	t.Logf("Completed install to dir %s", tempDir)

	envDir := filepath.Join(tempDir, "jenkins-x-dev-environment", "env")
	reqFile := filepath.Join(envDir, helm.RequirementsFileName)
	secretsFile := filepath.Join(envDir, helm.SecretsFileName)
	valuesFile := filepath.Join(envDir, helm.ValuesFileName)

	assert.FileExists(t, reqFile)
	assert.FileExists(t, secretsFile)
	assert.FileExists(t, valuesFile)

	req, err := helm.LoadRequirementsFile(reqFile)
	require.NoError(t, err)

	require.Equal(t, 1, len(req.Dependencies), "Number of dependencies in file %s", reqFile)
	dep0 := req.Dependencies[0]
	require.NotNil(t, dep0, "first dependency in file %s", reqFile)
	assert.Equal(t, cmd.DEFAULT_CHARTMUSEUM_URL, dep0.Repository, "requirement.dependency[0].Repository")
	assert.Equal(t, cmd.JENKINS_X_PLATFORM_CHART, dep0.Name, "requirement.dependency[0].Name")
	assert.NotEmpty(t, dep0.Version, "requirement.dependency[0].Version")

	values, err := chartutil.ReadValuesFile(valuesFile)
	require.NoError(t, err, "Failed to load values file", valuesFile)
	assertValuesHasPathValue(t, "values.yaml", values, "expose")

	secrets, err := chartutil.ReadValuesFile(secretsFile)
	require.NoError(t, err, "Failed to load secrets file", secretsFile)
	assertValuesHasPathValue(t, "secrets.yaml", secrets, "PipelineSecrets")


	// lets verify that we don't have any created resources in the cluster - as everything should be created in the file system
	assertNoEnvironments(t, jxClient, ns)

	_, cmNames, _ := kube.GetConfigMaps(kubeClient, ns)
	assert.Empty(t, cmNames, "Expected no ConfigMap names in namespace %s", ns)

	_, secretNames, _ := kube.GetSecrets(kubeClient, ns)
	assert.Empty(t, secretNames, "Expected no Secret names in namespace %s", ns)
}

func assertNoEnvironments(t *testing.T, jxClient versioned.Interface, ns string) {
	_, envNames, _ := kube.GetEnvironments(jxClient, ns)
	assert.Empty(t, envNames, "Expected no Environment names in namespace %s", ns)
}

// assertValuesHasPathValue asserts that the Values object has the given 
func assertValuesHasPathValue(t *testing.T, message string, values chartutil.Values, key string) (interface{}, error) {
	value, err := values.PathValue(key)
	if err != nil && value == nil {
		value = values.AsMap()[key]
		if value != nil {
			err = nil
		}
	}
	assert.NoError(t, err)
	assert.NotNil(t, value, "values does not contain entry for key", key, message)
	//t.Logf("%s has key %s with value %#v\n", message, key, value)
	return value, err
}
