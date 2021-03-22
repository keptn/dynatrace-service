package credentials

import (
	"errors"
	"fmt"
	"k8s.io/client-go/kubernetes"
	"os"
	"strings"

	"github.com/keptn-contrib/dynatrace-service/pkg/common"
	"github.com/keptn-contrib/dynatrace-service/pkg/config"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// DTCredentials is a struct for the tenant and api token information
type DTCredentials struct {
	Tenant   string `json:"DT_TENANT" yaml:"DT_TENANT"`
	ApiToken string `json:"DT_API_TOKEN" yaml:"DT_API_TOKEN"`
}

type KeptnAPICredentials struct {
	APIURL   string `json:"KEPTN_API_URL" yaml:"KEPTN_API_URL"`
	APIToken string `json:"KEPTN_API_TOKEN" yaml:"KEPTN_API_TOKEN"`
}

var namespace = getPodNamespace()

var ErrSecretNotFound = errors.New("secret not found")

func getPodNamespace() string {
	ns := os.Getenv("POD_NAMESPACE")
	if ns == "" {
		return "keptn"
	}

	return ns
}

type SecretReader interface {
	ReadSecret(secretName, namespace, secretKey string) (string, error)
}

type K8sCredentialReader struct {
	K8sClient kubernetes.Interface
}

func NewK8sCredentialReader(k8sClient kubernetes.Interface) (*K8sCredentialReader, error) {
	k8sCredentialReader := &K8sCredentialReader{}
	if k8sClient != nil {
		k8sCredentialReader.K8sClient = k8sClient
	} else {
		client, err := common.GetKubernetesClient()
		if err != nil {
			return nil, fmt.Errorf("could not initialize K8sCredentialReader: %s", err.Error())
		}
		k8sCredentialReader.K8sClient = client
	}
	return k8sCredentialReader, nil
}

func (kcr *K8sCredentialReader) ReadSecret(secretName, namespace, secretKey string) (string, error) {
	secret, err := kcr.K8sClient.CoreV1().Secrets(namespace).Get(secretName, metav1.GetOptions{})
	if err != nil {
		return "", err
	}
	if string(secret.Data[secretKey]) == "" {
		return "", ErrSecretNotFound
	}
	return string(secret.Data[secretKey]), nil
}

type OSEnvCredentialReader struct{}

func (OSEnvCredentialReader) ReadSecret(secretName, namespace, secretKey string) (string, error) {
	secret := os.Getenv(secretKey)
	if secret == "" {
		return secret, ErrSecretNotFound
	}
	return secret, nil
}

type CredentialManager struct {
	SecretReader SecretReader
}

func NewCredentialManager(sr SecretReader) (*CredentialManager, error) {
	cm := &CredentialManager{}
	if sr != nil {
		cm.SecretReader = sr
	} else {
		if common.RunLocal || common.RunLocalTest {
			cm.SecretReader = &OSEnvCredentialReader{}
			return cm, nil
		}
		sr, err := NewK8sCredentialReader(nil)
		if err != nil {
			return nil, fmt.Errorf("could not initialize CredentialManager: %s", err.Error())
		}
		cm.SecretReader = sr
	}
	return cm, nil
}

func (cm *CredentialManager) GetDynatraceCredentials(dynatraceConfig *config.DynatraceConfigFile) (*DTCredentials, error) {
	secretName := "dynatrace"
	if dynatraceConfig != nil && len(dynatraceConfig.DtCreds) > 0 {
		secretName = dynatraceConfig.DtCreds
	}

	dtTenant, err := cm.SecretReader.ReadSecret(secretName, namespace, "DT_TENANT")
	if err != nil {
		return nil, errors.New("invalid or no Dynatrace credentials found. Requires at least DT_TENANT and DT_API_TOKEN in secret!")
	}

	dtAPIToken, err := cm.SecretReader.ReadSecret(secretName, namespace, "DT_API_TOKEN")
	if err != nil {
		return nil, errors.New("invalid or no Dynatrace credentials found. Requires at least DT_TENANT and DT_API_TOKEN in secret!")
	}

	dtCreds := &DTCredentials{}

	dtCreds.Tenant = strings.Trim(dtTenant, "\n")
	// remove trailing slash since this causes errors with the API calls
	dtCreds.Tenant = strings.TrimSuffix(dtCreds.Tenant, "/")
	dtCreds.ApiToken = strings.Trim(dtAPIToken, "\n")

	return dtCreds, nil
}

func (cm *CredentialManager) GetKeptnAPICredentials() (*KeptnAPICredentials, error) {
	secretName := "dynatrace"

	apiURL, err := cm.SecretReader.ReadSecret(secretName, namespace, "KEPTN_API_URL")
	if err != nil {
		return nil, errors.New("invalid or no Keptn credentials found. Requires at least KEPTN_API_URL and KEPTN_API_TOKEN in secret!")
	}

	apiToken, err := cm.SecretReader.ReadSecret(secretName, namespace, "KEPTN_API_TOKEN")
	if err != nil {
		return nil, errors.New("invalid or no Keptn credentials found. Requires at least KEPTN_API_URL and KEPTN_API_TOKEN in secret!")
	}

	keptnCreds := &KeptnAPICredentials{}

	keptnCreds.APIURL = strings.Trim(apiURL, "\n")
	// remove trailing slash since this causes errors with the API calls
	keptnCreds.APIURL = strings.TrimSuffix(keptnCreds.APIURL, "/")
	keptnCreds.APIToken = strings.Trim(apiToken, "\n")

	if strings.HasPrefix(keptnCreds.APIURL, "http://") {
		return keptnCreds, nil
	}

	// ensure that apiURL uses https if no other protocol has explicitly been specified
	keptnCreds.APIURL = strings.TrimPrefix(keptnCreds.APIURL, "https://")
	keptnCreds.APIURL = "https://" + keptnCreds.APIURL

	return keptnCreds, nil
}

// GetDynatraceCredentials reads the Dynatrace credentials from the secret. Therefore, it first checks
// if a secret is specified in the dynatrace.conf.yaml and if not defaults to the secret "dynatrace"
func GetDynatraceCredentials(dynatraceConfig *config.DynatraceConfigFile) (*DTCredentials, error) {

	cm, err := NewCredentialManager(nil)
	if err != nil {
		return nil, err
	}
	return cm.GetDynatraceCredentials(dynatraceConfig)
}

// GetKeptnCredentials retrieves the Keptn Credentials from the "dynatrace" secret
func GetKeptnCredentials() (*KeptnAPICredentials, error) {

	cm, err := NewCredentialManager(nil)
	if err != nil {
		return nil, err
	}
	return cm.GetKeptnAPICredentials()
}
