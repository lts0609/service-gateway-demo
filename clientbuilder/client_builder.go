package clientbuilder

import (
	clientset "k8s.io/client-go/kubernetes"
	restclient "k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

type ClientBuilder interface {
	Config() (*restclient.Config, error)
	Client() (clientset.Interface, error)
}

type ClientBuilderImpl struct {
	ClientConfig *restclient.Config
}

func NewClientBuilder() (*ClientBuilderImpl, error) {
	kubeconfig, err := clientcmd.BuildConfigFromFlags("", "")
	// use in local test
	//kubeconfig, err := clientcmd.BuildConfigFromFlags("", "/root/.kube/config")
	if err != nil {
		return nil, err
	}
	return &ClientBuilderImpl{
		ClientConfig: kubeconfig,
	}, nil
}

// Get the rest config
func (c ClientBuilderImpl) Config() (*restclient.Config, error) {
	config := c.ClientConfig
	return restclient.AddUserAgent(config, "pod-creator"), nil
}

// Get the root client
func (c ClientBuilderImpl) Client() (clientset.Interface, error) {
	config, err := c.Config()
	if err != nil {
		return nil, err
	}
	return clientset.NewForConfig(config)
}
