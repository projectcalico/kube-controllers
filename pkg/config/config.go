package config

import (
	"io/ioutil"
	"os"
	"strings"

	log "github.com/Sirupsen/logrus"
	"github.com/kelseyhightower/envconfig"
	"k8s.io/client-go/pkg/api"
	"k8s.io/client-go/rest"
	certutil "k8s.io/client-go/util/cert"
)

type Config struct {
	K8sApi            string `default:"https://kubernetes.default:443" split_words:"true"`
	K8sAuthToken      string `split_words:"true"`
	K8sServiceHost    string `default:"10.100.0.1" split_words:"true"`
	ElectionUrl       string `default:"http://127.0.0.1:4040/" split_words:"true"`
	LeaderElection    string `default:"false" split_words:"true"`
	Hostname          string
	LogLevel          string `default:"info" split_words:"true"`
	ConfigureEtcHosts string `default:"false" split_words:"true"`
}

func (c *Config) Parse() error {
	return envconfig.Process("", c)
}

func (c *Config) K8sClusterConfig() (*rest.Config, error) {

	if len(c.K8sAuthToken) == 0 {
		token, err := ioutil.ReadFile("/var/run/secrets/kubernetes.io/serviceaccount/" + api.ServiceAccountTokenKey)
		if err == nil {
			c.K8sAuthToken = string(token)
		} else {
			log.Warn("No service account token found on disk")
		}
	}

	tlsClientConfig := rest.TLSClientConfig{}
	rootCAFile := "/var/run/secrets/kubernetes.io/serviceaccount/" + api.ServiceAccountRootCAKey
	if _, err := certutil.NewPool(rootCAFile); err != nil {
		tlsClientConfig.Insecure = true
		log.Errorf("Expected to load root CA config from %s, but got err: %v", rootCAFile, err)
	} else {
		tlsClientConfig.CAFile = rootCAFile
	}

	return &rest.Config{
		Host:            c.K8sApi,
		BearerToken:     c.K8sAuthToken,
		TLSClientConfig: tlsClientConfig,
	}, nil
}

func (c *Config) ConfigEtcHostsIfRequired() {
	if c.ConfigureEtcHosts == "true" {
		f, err := os.OpenFile("/etc/hosts", os.O_APPEND|os.O_WRONLY, 0600)
		if err != nil {
			panic(err)
		}
		defer f.Close()
		if _, err = f.WriteString(strings.Join([]string{c.Hostname, "kubernetes.default\n"}, "\t")); err != nil {
			panic(err)
		}
		log.Infof("Appended 'kubernetes.default' -> %s to /etc/hosts", c.Hostname)
	}
}
