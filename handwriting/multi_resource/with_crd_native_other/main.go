// Package main
// @Description:
package main

import (
	"context"
	"flag"
	"k8s-controller/handwriting/with-crd/generated/clientset/versioned"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/tools/leaderelection"
	"k8s.io/client-go/tools/leaderelection/resourcelock"
	"k8s.io/klog/v2"
	"os"
	"os/signal"
	"syscall"
	"time"
)

const (
	Namespace                = "default"
	ControllerFooName        = "foo-controller"
	ControllerCiliumNodeName = "foo-controller"
)

var (
	kubeconfig           string
	master               string
	onlyOneSignalHandler = make(chan struct{})
	shutdownSignals      = []os.Signal{os.Interrupt, syscall.SIGTERM}
)

// SetupSignalHandler registered for SIGTERM and SIGINT. A context is returned
// which is cancelled on one of these signals. If a second signal is caught,
// the program is terminated with exit code 1.
func SetupSignalHandler() context.Context {
	close(onlyOneSignalHandler) // panics when called twice

	c := make(chan os.Signal, 2)
	ctx, cancel := context.WithCancel(context.Background())
	signal.Notify(c, shutdownSignals...)
	go func() {
		<-c
		cancel()
		<-c
		os.Exit(1) // second signal. Exit directly.
	}()

	return ctx
}

// getResourceLock creates a new resource lock
func getResourceLock(client *kubernetes.Clientset) (resourcelock.Interface, error) {
	lockName := "foo-controller-lock"
	lockNamespace := Namespace
	identity, err := os.Hostname()
	if err != nil {
		return nil, err
	}

	return resourcelock.New(
		resourcelock.LeasesResourceLock,
		lockNamespace,
		lockName,
		client.CoreV1(),
		client.CoordinationV1(),
		resourcelock.ResourceLockConfig{
			Identity: identity,
		},
	)
}

func main() {
	flag.StringVar(&kubeconfig, "kubeconfig", "", "absolute path to the kubeconfig file")
	flag.StringVar(&master, "master", "", "master url")
	flag.Parse()

	// creates the connection
	var config *rest.Config
	var err error
	if kubeconfig == "" && master == "" {
		config, err = rest.InClusterConfig()
		if err != nil {
			klog.Fatal(err)
		}
	} else {
		config, err = clientcmd.BuildConfigFromFlags(master, kubeconfig)
		if err != nil {
			klog.Fatal(err)
		}
	}

	// creates the clientset for our custom resource
	fooClientset, err := versioned.NewForConfig(config)
	if err != nil {
		klog.Fatal(err)
	}

	// creates the clientset
	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		klog.Fatal(err)
	}

	//create dynamic client
	dynamicClient, err := dynamic.NewForConfig(config)
	if err != nil {
		klog.Fatal(err)
	}

	// set up signals so we handle the shutdown signal gracefully
	ctx := SetupSignalHandler()
	controller := NewController(ctx, clientset, fooClientset)
	ciliumNodeController := NewCiliumNodeController(ctx, dynamicClient, clientset)

	rl, err := getResourceLock(clientset)
	if err != nil {
		klog.Fatal(err)
	}
	leaderelection.RunOrDie(ctx, leaderelection.LeaderElectionConfig{
		Lock:          rl,
		LeaseDuration: 60 * time.Second,
		RenewDeadline: 15 * time.Second,
		RetryPeriod:   5 * time.Second,
		Callbacks: leaderelection.LeaderCallbacks{
			OnStartedLeading: func(ctx context.Context) {
				logger := klog.FromContext(ctx)
				//同步缓存并启动调谐
				controller.startInformer(ctx)
				go controller.Run(ctx, 1)
				//同步缓存并启动调谐
				ciliumNodeController.startClientInformer(ctx)
				go ciliumNodeController.Run(ctx, 1)
				//关闭queen
				defer func() {
					// Let the workers stop when we are done
					controller.fooQueue.ShutDown()
					controller.deployQueen.ShutDown()
					// Let the workers stop when we are done
					ciliumNodeController.ciliumNodeQueue.ShutDown()
				}()
				logger.Info("Started workers")
				// Wait until the context is cancelled
				<-ctx.Done()
				logger.Info("Shutting down workers")
			},
			OnStoppedLeading: func() {
				klog.Info("leaderelection lost")
			},
			OnNewLeader: func(identity string) {
				if identity == rl.Identity() {
					klog.Info("leaderelection won")
				}
			},
		},
	})

	//informerFactory.Start(ctx.Done())
	//
	//// Now let's start the controller
	//controller.Run(ctx, 1)
}
