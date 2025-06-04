package main

import (
	"context"
	"fmt"
	appsv1 "k8s.io/api/apps/v1"
	v1 "k8s.io/api/core/v1"
	"k8s.io/klog/v2"
	"net/http"
	"net/http/httputil"
	"net/url"
	"regexp"
	"service-proxy-demo/clientbuilder"
	"strings"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	clientset "k8s.io/client-go/kubernetes"
)

func main() {
	klog.InitFlags(nil)
	klog.Errorf("Service Gateway Starting")
	clientBuilder, err := clientbuilder.NewClientBuilder()
	if err != nil {
		klog.Errorf("Failed to create client builder: %v", err)
	}
	client, err := clientBuilder.Client()
	if err != nil {
		klog.Errorf("Failed to create client: %v", err)
	}

	var ListenAddress string
	// 创建一个带有自定义 Director 的反向代理
	proxy := &httputil.ReverseProxy{
		Director: func(req *http.Request) {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()

			deploymentName, err := extractFromRequestURL(req.URL.Path)
			if err != nil {
				klog.Errorf("Error extracting deployment from url: %v", err)
				req.URL.Path = "/error"
				return
			}

			deployment, err := findDeploymentObject(ctx, client, deploymentName)
			if err != nil {
				klog.Errorf("Error finding deployment: %v", err)
				req.URL.Path = "/error"
				return
			}

			service, err := findServiceByDeploymentLabels(ctx, client, deployment)
			if err != nil {
				klog.Errorf("Error finding service url: %v", err)
				req.URL.Path = "/error"
				return
			}

			serviceUrl, err := buildServiceUrl(ctx, service)
			if err != nil {
				klog.Errorf("Error building service url: %v", err)
				req.URL.Path = "/error"
				return
			}

			newPath := strings.TrimPrefix(req.URL.Path, fmt.Sprintf("/instance/%s", deploymentName))
			if newPath == "" {
				newPath = "/"
			}
			req.URL.Path = newPath
			req.URL.Scheme = serviceUrl.Scheme
			req.URL.Host = serviceUrl.Host

			// 设置必要的请求头
			if _, ok := req.Header["User-Agent"]; !ok {
				req.Header.Set("User-Agent", "")
			}
			klog.Errorf("Proxying request to %s%s", req.URL.Host, req.URL.Path)
		},
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			klog.Errorf("Proxy error: %v", err)
			http.Error(w, "Proxy error occurred", http.StatusInternalServerError)
		},
	}

	ListenAddress = ":8080"
	mux := http.NewServeMux()
	mux.Handle("/instance/", proxy)
	mux.HandleFunc("/error", func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "Backend service not found", http.StatusNotFound)
	})

	klog.Errorf("Starting proxy server on %s", ListenAddress)
	klog.Fatal(http.ListenAndServe(ListenAddress, mux))
}

func extractFromRequestURL(path string) (string, error) {
	re := regexp.MustCompile(`^/instance/([^/]+)(/.*)?$`)
	matches := re.FindStringSubmatch(path)

	if len(matches) < 2 {
		return "", fmt.Errorf("invalid path format: %s", path)
	}

	return matches[1], nil
}

func findDeploymentObject(ctx context.Context, client clientset.Interface, deploymentName string) (*appsv1.Deployment, error) {
	selector := fmt.Sprintf("metadata.name=%s", deploymentName)
	deployments, err := client.AppsV1().Deployments(metav1.NamespaceAll).List(ctx, metav1.ListOptions{
		FieldSelector: selector,
	})
	if err != nil {
		return nil, err
	}

	if len(deployments.Items) == 0 {
		return nil, fmt.Errorf("deployment %s not found", deploymentName)
	}

	return &deployments.Items[0], nil
}

func findServiceByDeploymentLabels(ctx context.Context, client clientset.Interface, depolyment *appsv1.Deployment) (*v1.Service, error) {
	selector := metav1.FormatLabelSelector(depolyment.Spec.Selector)
	if selector == "" {
		return nil, fmt.Errorf("no selector in deployment", depolyment.Name)
	}

	service, err := client.CoreV1().Services(depolyment.Namespace).List(ctx, metav1.ListOptions{
		LabelSelector: selector,
	})
	if err != nil {
		return nil, err
	}
	if len(service.Items) == 0 {
		return nil, fmt.Errorf("no service found in deployment")
	}
	return &service.Items[0], nil
}

func buildServiceUrl(ctx context.Context, service *v1.Service) (*url.URL, error) {
	if len(service.Spec.Ports) == 0 {
		return nil, fmt.Errorf("service %s has no ports defined", service.Name)
	}

	var servicePort *v1.ServicePort
	serviceHost := fmt.Sprintf("%s.%s.svc.cluster.local", service.Name, service.Namespace)
	for _, port := range service.Spec.Ports {
		if port.Name == "http" {
			servicePort = &port
			break
		}
	}
	if servicePort == nil {
		servicePort = &service.Spec.Ports[0]
	}

	return &url.URL{
		Scheme: "http",
		Host:   fmt.Sprintf("%s:%d", serviceHost, servicePort),
	}, nil
}
