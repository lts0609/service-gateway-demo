package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net/http"
	"net/http/httputil"
	"net/url"
	"regexp"
	"strings"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

// 配置参数
type Config struct {
	Kubeconfig    string
	ListenAddress string
	Namespace     string
}

func main() {
	config := parseFlags()
	clientset := createKubernetesClient(config.Kubeconfig, config.Namespace)

	// 创建一个带有自定义 Director 的反向代理
	proxy := &httputil.ReverseProxy{
		Director: func(req *http.Request) {
			// 从请求路径中提取 deployment 名称
			deploymentName, err := extractDeploymentName(req.URL.Path)
			if err != nil {
				log.Printf("Error extracting deployment name: %v", err)
				req.URL.Path = "/error"
				return
			}

			// 根据 deployment 名称查找对应的 service
			serviceURL, err := findServiceURL(clientset, config.Namespace, deploymentName)
			if err != nil {
				log.Printf("Error finding service: %v", err)
				req.URL.Path = "/error"
				return
			}

			// 设置代理目标
			req.URL.Scheme = serviceURL.Scheme
			req.URL.Host = serviceURL.Host
			// 调整路径，移除 /instance/ 前缀
			newPath := strings.TrimPrefix(req.URL.Path, fmt.Sprintf("/instance/%s", deploymentName))
			if newPath == "" {
				newPath = "/"
			}
			req.URL.Path = newPath

			// 设置必要的请求头
			if _, ok := req.Header["User-Agent"]; !ok {
				req.Header.Set("User-Agent", "")
			}
			log.Printf("Proxying request to %s%s", req.URL.Host, req.URL.Path)
		},
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			log.Printf("Proxy error: %v", err)
			http.Error(w, "Proxy error occurred", http.StatusInternalServerError)
		},
	}

	// 注册路由
	mux := http.NewServeMux()
	mux.Handle("/instance/", proxy)
	mux.HandleFunc("/error", func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "Service not found", http.StatusNotFound)
	})

	// 启动服务器
	log.Printf("Starting proxy server on %s", config.ListenAddress)
	log.Fatal(http.ListenAndServe(config.ListenAddress, mux))
}

// 解析命令行参数
func parseFlags() Config {
	var config Config
	flag.StringVar(&config.Kubeconfig, "kubeconfig", "", "Path to kubeconfig file")
	flag.StringVar(&config.ListenAddress, "listen", ":8080", "Address to listen on")
	flag.StringVar(&config.Namespace, "namespace", "default", "Kubernetes namespace")
	flag.Parse()
	return config
}

// 创建 Kubernetes 客户端
func createKubernetesClient(kubeconfigPath, namespace string) *kubernetes.Clientset {
	var config *rest.Config
	var err error

	if kubeconfigPath != "" {
		// 使用指定的 kubeconfig 文件
		config, err = clientcmd.BuildConfigFromFlags("", kubeconfigPath)
	} else {
		// 尝试在集群内使用服务账户
		config, err = rest.InClusterConfig()
	}

	if err != nil {
		log.Fatalf("Failed to create Kubernetes client: %v", err)
	}

	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		log.Fatalf("Failed to create clientset: %v", err)
	}

	// 测试连接
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, err = clientset.CoreV1().Namespaces().Get(ctx, namespace, metav1.GetOptions{})
	if err != nil {
		log.Fatalf("Failed to access namespace %s: %v", namespace, err)
	}

	return clientset
}

// 从路径中提取 deployment 名称
func extractDeploymentName(path string) (string, error) {
	// 匹配 /instance/<deployment-name>/... 格式的路径
	re := regexp.MustCompile(`^/instance/([^/]+)(/.*)?$`)
	matches := re.FindStringSubmatch(path)

	if len(matches) < 2 {
		return "", fmt.Errorf("invalid path format: %s", path)
	}

	return matches[1], nil
}

// 根据 deployment 名称查找对应的 service
func findServiceURL(clientset *kubernetes.Clientset, namespace, deploymentName string) (*url.URL, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// 获取 service
	service, err := clientset.CoreV1().Services(namespace).Get(ctx, deploymentName, metav1.GetOptions{})
	if err != nil {
		return nil, fmt.Errorf("service %s not found: %v", deploymentName, err)
	}

	// 构建 service URL
	serviceHost := fmt.Sprintf("%s.%s.svc.cluster.local", service.Name, service.Namespace)

	// 默认使用第一个端口
	if len(service.Spec.Ports) == 0 {
		return nil, fmt.Errorf("service %s has no ports defined", service.Name)
	}

	port := service.Spec.Ports[0].Port
	serviceURL := &url.URL{
		Scheme: "http",
		Host:   fmt.Sprintf("%s:%d", serviceHost, port),
	}

	return serviceURL, nil
}
