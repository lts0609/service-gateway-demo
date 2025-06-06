package main

import (
	"context"
	"fmt"
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
	"k8s.io/apimachinery/pkg/labels"
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

			podName, err := extractFromRequestURL(req.URL.Path)
			if err != nil {
				klog.Errorf("Error extracting deployment from url: %v", err)
				req.URL.Path = "/error"
				return
			}

			pod, err := DiscoverPodbyName(ctx, client, podName)
			if err != nil {
				klog.Errorf("Error get pod: %v", err)
				req.URL.Path = "/error"
				return
			}

			service, err := findServiceByPodLabels(ctx, client, pod)
			if err != nil {
				klog.Errorf("Error finding service by leabels: %v", err)
				req.URL.Path = "/error"
				return
			}

			serviceUrl, err := buildServiceUrl(ctx, service)
			if err != nil {
				klog.Errorf("Error building service url: %v", err)
				req.URL.Path = "/error"
				return
			}

			newPath := strings.TrimPrefix(req.URL.Path, fmt.Sprintf("/instance/%s", podName))
			if newPath == "" {
				newPath = "/"
			}
			req.URL.Path = newPath
			req.URL.Scheme = serviceUrl.Scheme
			req.URL.Host = serviceUrl.Host
			klog.Errorf("req is %v", req)
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

func DiscoverPodbyName(ctx context.Context, client clientset.Interface, podName string) (*v1.Pod, error) {
	pods, err := client.CoreV1().Pods(v1.NamespaceAll).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, err
	}

	for _, pod := range pods.Items {
		if pod.Name == podName {
			return &pod, nil
		}
	}

	return nil, fmt.Errorf("pod %s not found", podName)
}

func findServiceByPodLabels(ctx context.Context, client clientset.Interface, pod *v1.Pod) (*v1.Service, error) {
	podLabel := pod.Labels
	if podLabel == nil {
		return nil, fmt.Errorf("pod %s has no label", pod.Name)
	}
	labelSet := labels.Set(pod.Labels)

	services, err := client.CoreV1().Services(pod.Namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, err
	}
	if len(services.Items) == 0 {
		return nil, fmt.Errorf("no service found in namespace %s", pod.Namespace)
	}
	for _, service := range services.Items {
		selector, err := metav1.LabelSelectorAsSelector(&metav1.LabelSelector{
			MatchLabels: service.Spec.Selector,
		})
		if err != nil {
			return nil, err
		}
		if !selector.Empty() && selector.Matches(labelSet) {
			return &service, nil
		}
	}
	return nil, nil
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
		Host:   fmt.Sprintf("%s:%d", serviceHost, servicePort.Port),
	}, nil
}

func isRedirectStatusCode(code int) bool {
	return code == http.StatusMovedPermanently ||
		code == http.StatusFound ||
		code == http.StatusSeeOther ||
		code == http.StatusNotModified ||
		code == http.StatusUseProxy ||
		code == http.StatusTemporaryRedirect ||
		code == http.StatusPermanentRedirect
}
