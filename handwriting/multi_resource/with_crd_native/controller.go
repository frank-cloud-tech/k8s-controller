// Package main
// @Description:
package main

import (
	"context"
	"flag"
	"fmt"
	"golang.org/x/time/rate"
	"k8s-controller/handwriting/with_crd/apis/foo/v1alpha1"
	"k8s-controller/handwriting/with_crd/generated/clientset/versioned"
	versionedScheme "k8s-controller/handwriting/with_crd/generated/clientset/versioned/scheme"
	v12 "k8s.io/api/apps/v1"
	appsV1Informer "k8s.io/client-go/informers/apps/v1"

	"k8s-controller/handwriting/with_crd/generated/informers/externalversions"
	InformerV1alpha1 "k8s-controller/handwriting/with_crd/generated/informers/externalversions/foo/v1alpha1"
	listerV1alpha1 "k8s-controller/handwriting/with_crd/generated/listers/foo/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes/scheme"
	v1 "k8s.io/client-go/kubernetes/typed/core/v1"
	appsV1 "k8s.io/client-go/listers/apps/v1"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/leaderelection"
	"k8s.io/client-go/tools/leaderelection/resourcelock"
	"k8s.io/klog/v2"
	"os"
	"os/signal"
	"syscall"
	"time"

	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/tools/record"
	"k8s.io/client-go/util/workqueue"
)

const (
	Namespace           = "default"
	ControllerAgentName = "foo-controller"
)

// Controller demonstrates how to implement a controller with client-go.
type Controller struct {
	recorder record.EventRecorder

	//custom
	fooSharedInformerFactory externalversions.SharedInformerFactory //k8s.io/code-generator自动生成的sharedFactory
	fooQueue                 workqueue.RateLimitingInterface
	fooLister                listerV1alpha1.FooLister     //k8s.io/code-generator自动生成的lister
	fooInformer              InformerV1alpha1.FooInformer //k8s.io/code-generator自动生成的informer

	//resource
	resSharedInformerFactory informers.SharedInformerFactory
	deployQueen              workqueue.RateLimitingInterface
	deployLister             appsV1.DeploymentLister
	deployInformer           appsV1Informer.DeploymentInformer
}

// NewController creates a new Controller.
func NewController(ctx context.Context, clientset *kubernetes.Clientset, fooClientset versioned.Interface) *Controller {

	logger := klog.FromContext(ctx)
	logger.Info("Starting Foo controller")

	/*
		custom resource Foo
	*/
	// create an informer factory
	fooInformerFactory := externalversions.NewSharedInformerFactory(fooClientset, time.Second*30)

	// create an informer and lister for foos
	fooInformer := fooInformerFactory.Frank().V1alpha1().Foos()
	fooLister := fooInformerFactory.Frank().V1alpha1().Foos().Lister()

	// create the workqueue
	ratelimiter := workqueue.NewMaxOfRateLimiter(
		workqueue.NewItemExponentialFailureRateLimiter(5*time.Millisecond, 1000*time.Second),
		&workqueue.BucketRateLimiter{Limiter: rate.NewLimiter(rate.Limit(50), 300)},
	)
	fooQueue := workqueue.NewRateLimitingQueue(ratelimiter)

	// register the event handler with the informer
	fooInformer.Informer().AddEventHandler(cache.ResourceEventHandlerFuncs{
		// AddFunc is called when a new foo object has been added to the store.
		AddFunc: func(obj interface{}) {
			key, err := cache.MetaNamespaceKeyFunc(obj)
			if err == nil {
				fooQueue.Add(key)
			}
		},
		// UpdateFunc is called when an existing foo object has been updated.
		// 同步处理, 如果这里发生阻塞, 那么后续的事件将不会被处理(建议将对象放到queen中统一处理)
		UpdateFunc: func(old, new interface{}) {
			oldFoo := old.(*v1alpha1.Foo)
			newFoo := new.(*v1alpha1.Foo)
			if oldFoo.ResourceVersion != newFoo.ResourceVersion {
				logger.Info("a foo updated", "foo", oldFoo.Name, "oldVersion", oldFoo.ResourceVersion, "newVersion", newFoo.ResourceVersion)
			}
		},
		DeleteFunc: func(obj interface{}) {
			// IndexerInformer uses a delta queue, therefore for deletes we have to use this
			// key function.
			key, err := cache.DeletionHandlingMetaNamespaceKeyFunc(obj)
			if err == nil {
				fooQueue.Add(key)
			}
		},
	})

	/*
		original resource Deployment
	*/
	resInformerFactory := informers.NewSharedInformerFactory(clientset, time.Second*30)
	deployInformer := resInformerFactory.Apps().V1().Deployments()
	deployLister := resInformerFactory.Apps().V1().Deployments().Lister()

	// create the workqueue
	deployRatelimiter := workqueue.NewMaxOfRateLimiter(
		workqueue.NewItemExponentialFailureRateLimiter(5*time.Millisecond, 1000*time.Second),
		&workqueue.BucketRateLimiter{Limiter: rate.NewLimiter(rate.Limit(50), 300)},
	)
	deployQueue := workqueue.NewRateLimitingQueue(deployRatelimiter)

	deployInformer.Informer().AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc: func(obj interface{}) {
			key, err := cache.MetaNamespaceKeyFunc(obj)
			if err == nil {
				deployQueue.Add(key)
			}
		},
		UpdateFunc: func(old, new interface{}) {
			oldDeploy := old.(*v12.Deployment)
			newDeploy := new.(*v12.Deployment)
			if oldDeploy.ResourceVersion != newDeploy.ResourceVersion {
				logger.Info("a deploy updated", "deploy", oldDeploy.Name, "oldVersion", oldDeploy.ResourceVersion, "newVersion", newDeploy.ResourceVersion)
			}
		},
		DeleteFunc: func(obj interface{}) {
			key, err := cache.DeletionHandlingMetaNamespaceKeyFunc(obj)
			if err == nil {
				deployQueue.Add(key)
			}

		},
	})

	// Create event broadcaster
	// Add foo types to the default Kubernetes Scheme so Events can be
	// logged for foo types.
	utilruntime.Must(versionedScheme.AddToScheme(scheme.Scheme))
	logger.V(4).Info("Creating event broadcaster")

	eventBroadcaster := record.NewBroadcaster()
	eventBroadcaster.StartStructuredLogging(0)
	eventBroadcaster.StartRecordingToSink(&v1.EventSinkImpl{Interface: clientset.CoreV1().Events("")})
	recorder := eventBroadcaster.NewRecorder(scheme.Scheme, corev1.EventSource{Component: ControllerAgentName})

	return &Controller{
		recorder: recorder,
		//custom
		fooSharedInformerFactory: fooInformerFactory,
		fooQueue:                 fooQueue,
		fooLister:                fooLister,
		fooInformer:              fooInformer,
		//resource
		resSharedInformerFactory: resInformerFactory,
		deployQueen:              deployQueue,
		deployLister:             deployLister,
		deployInformer:           deployInformer,
	}
}

// startInformer
// @Description: 启动informer, 和apiserver建立长连接, 从apiserver获取资源变化
// @receiver c
// @param ctx
func (c *Controller) startInformer(ctx context.Context) {
	c.fooSharedInformerFactory.Start(ctx.Done())
	c.resSharedInformerFactory.Start(ctx.Done())
}

// processFooNextItem
// @Description: 从队列中取出key, 并处理key对应的资源
// @receiver c
// @param ctx
// @return bool
func (c *Controller) processFooNextItem(ctx context.Context) bool {
	// Wait until there is a new item in the working queue
	key, quit := c.fooQueue.Get() //从队列中拿出key, 如果队列中没有key则阻塞, 直到有key
	logger := klog.FromContext(ctx)
	logger.Info("processNextItem")
	if quit {
		logger.Info("quit")
		return false
	}
	// Tell the queue that we are done with processing this key. This unblocks the key for other workers
	// This allows safe parallel processing because two foos with the same key are never processed in
	// parallel.
	defer c.fooQueue.Done(key)

	// Invoke the method containing the business logic
	err := c.syncFooToStdout(ctx, key.(string))
	// Handle the error if something went wrong during the execution of the business logic
	c.handleErr(err, key)
	return true
}

// processDeployNextItem
// @Description: 从队列中取出key, 并处理key对应的资源
// @receiver c
// @param ctx
// @return bool
func (c *Controller) processDeployNextItem(ctx context.Context) bool {
	// Wait until there is a new item in the working queue
	key, quit := c.deployQueen.Get() //从队列中拿出key, 如果队列中没有key则阻塞, 直到有key
	logger := klog.FromContext(ctx)
	logger.Info("processNextItem")
	if quit {
		logger.Info("quit")
		return false
	}
	// Tell the queue that we are done with processing this key. This unblocks the key for other workers
	// This allows safe parallel processing because two deployments with the same key are never processed in
	// parallel.
	defer c.deployQueen.Done(key)

	// Invoke the method containing the business logic
	err := c.syncDeployToStdout(ctx, key.(string))
	// Handle the error if something went wrong during the execution of the business logic
	c.handleErr(err, key)
	return true
}

// handleErr checks if an error happened and makes sure we will retry later.
func (c *Controller) handleErr(err error, key interface{}) {
	if err == nil {
		// Forget about the #AddRateLimited history of the key on every successful synchronization.
		// This ensures that future processing of updates for this key is not delayed because of
		// an outdated error history.
		c.fooQueue.Forget(key)
		return
	}

	// This controller retries 5 times if something goes wrong. After that, it stops trying.
	if c.fooQueue.NumRequeues(key) < 5 {
		klog.Infof("Error syncing foo %v: %v", key, err)

		// Re-enqueue the key rate limited. Based on the rate limiter on the
		// queue and the re-enqueue history, the key will be processed later again.
		c.fooQueue.AddRateLimited(key)
		return
	}

	c.fooQueue.Forget(key)
	// Report to an external entity that, even after several retries, we could not successfully process this key
	utilruntime.HandleError(err)
	klog.Infof("Dropping foo %q out of the queue: %v", key, err)
}

// syncToStdout is the business logic of the controller. In this controller it simply prints
// information about the foo to stdout. In case an error happened, it has to simply return the error.
// The retry logic should not be part of the business logic.
func (c *Controller) syncFooToStdout(ctx context.Context, key string) error {
	logger := klog.LoggerWithValues(klog.FromContext(ctx), "resourceName", key)
	logger.Info("syncToStdout")
	namespace, name, err := cache.SplitMetaNamespaceKey(key)

	if err != nil {
		utilruntime.HandleError(fmt.Errorf("invalid resource key: %s", key))
		return nil
	}

	// Get the Foo resource with this namespace/name
	foo, err := c.fooLister.Foos(namespace).Get(name)
	if err != nil {
		if errors.IsNotFound(err) {
			utilruntime.HandleError(fmt.Errorf("foo '%s' in work queue no longer exists", key))
			return nil
		}

		return err
	}

	// Note that you also have to check the uid if you have a local controlled resource, which
	// is dependent on the actual instance, to detect that a Foo was recreated with the same name
	logger.Info("Sync/Add/Update for Foo", "foo", foo.GetName())
	c.recorder.Event(foo, corev1.EventTypeNormal, "Synced", "Foo synced successfully")

	return nil
}

// syncToStdout is the business logic of the controller. In this controller it simply prints
// information about the foo to stdout. In case an error happened, it has to simply return the error.
// The retry logic should not be part of the business logic.
func (c *Controller) syncDeployToStdout(ctx context.Context, key string) error {
	logger := klog.LoggerWithValues(klog.FromContext(ctx), "resourceName", key)
	logger.Info("syncToStdout")
	namespace, name, err := cache.SplitMetaNamespaceKey(key)

	if err != nil {
		utilruntime.HandleError(fmt.Errorf("invalid resource key: %s", key))
		return nil
	}

	// Get the Foo resource with this namespace/name
	deploy, err := c.deployLister.Deployments(namespace).Get(name)
	if err != nil {
		if errors.IsNotFound(err) {
			utilruntime.HandleError(fmt.Errorf("deploy '%s' in work queue no longer exists", key))
			return nil
		}

		return err
	}

	// Note that you also have to check the uid if you have a local controlled resource, which
	// is dependent on the actual instance, to detect that a Foo was recreated with the same name
	logger.Info("Sync/Add/Update for Deploy", "deploy", deploy.GetName())
	c.recorder.Event(deploy, corev1.EventTypeNormal, "Synced", "Deploy synced successfully")

	return nil
}

func (c *Controller) runFooWorker(ctx context.Context) {
	for c.processFooNextItem(ctx) {
	}
}

func (c *Controller) runDeployWorker(ctx context.Context) {
	for c.processDeployNextItem(ctx) {
	}
}

// Run begins watching and syncing.
func (c *Controller) Run(ctx context.Context, workers int) {
	defer utilruntime.HandleCrash()
	// Let the workers stop when we are done
	defer c.fooQueue.ShutDown()
	defer c.deployQueen.ShutDown()
	logger := klog.FromContext(ctx)
	logger.Info("Starting Foo controller")

	// Wait for all involved caches to be synced, before processing items from the queue is started
	if !cache.WaitForCacheSync(ctx.Done(), c.fooInformer.Informer().HasSynced) {
		utilruntime.HandleError(fmt.Errorf("Timed out waiting for caches to sync"))
		return
	}

	// Wait for all involved caches to be synced, before processing items from the queue is started
	if !cache.WaitForCacheSync(ctx.Done(), c.deployInformer.Informer().HasSynced) {
		utilruntime.HandleError(fmt.Errorf("Timed out waiting for caches to sync"))
		return
	}

	// Launch workers to process Foo resources
	for i := 0; i < workers; i++ {
		go wait.UntilWithContext(ctx, c.runFooWorker, time.Second)
		go wait.UntilWithContext(ctx, c.runDeployWorker, time.Second)
	}

	logger.Info("Started workers")
	// Wait until the context is cancelled
	<-ctx.Done()
	logger.Info("Shutting down workers")
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

var onlyOneSignalHandler = make(chan struct{})
var shutdownSignals = []os.Signal{os.Interrupt, syscall.SIGTERM}

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

var kubeconfig string
var master string

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

	// set up signals so we handle the shutdown signal gracefully
	ctx := SetupSignalHandler()
	controller := NewController(ctx, clientset, fooClientset)

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
				controller.startInformer(ctx)
				controller.Run(ctx, 1)
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
