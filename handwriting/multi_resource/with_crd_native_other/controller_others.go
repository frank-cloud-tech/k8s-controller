// Package main
// @Description:
package main

import (
	"context"
	"encoding/json"
	"fmt"
	versionedScheme "k8s-controller/handwriting/with-crd/generated/clientset/versioned/scheme"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic/dynamicinformer"
	"k8s.io/client-go/kubernetes"

	cilium_api_v2 "github.com/cilium/cilium/pkg/k8s/apis/cilium.io/v2"
	"k8s.io/client-go/dynamic"

	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/kubernetes/scheme"
	v1 "k8s.io/client-go/kubernetes/typed/core/v1"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/tools/record"
	"k8s.io/client-go/util/workqueue"
	"k8s.io/klog/v2"
	"time"
)

var ciliumNodesResource = schema.GroupVersionResource{Group: "cilium.io", Version: "v2", Resource: "ciliumnodes"}

// CiliumNodeController demonstrates how to implement a controller with client-go.
type CiliumNodeController struct {
	recorder           record.EventRecorder
	ciliumNodeInformer cache.SharedIndexInformer
	ciliumNodeInLister cache.GenericLister
	ciliumNodeQueue    workqueue.RateLimitingInterface
}

// NewCiliumNodeController creates a new Controller.
// note: use dynamic.Interface to create informer factory
func NewCiliumNodeController(ctx context.Context, client dynamic.Interface, clientset *kubernetes.Clientset) *CiliumNodeController {
	logger := klog.FromContext(ctx)
	/*
		other resource CiliumNode
	*/
	// create an informer factory
	dynInformerFactory := dynamicinformer.NewDynamicSharedInformerFactory(client, 0)
	ciliumNodeInformer := dynInformerFactory.ForResource(ciliumNodesResource).Informer()
	ciliumNodeInLister := dynInformerFactory.ForResource(ciliumNodesResource).Lister()

	ciliumNodeQueue := workqueue.NewRateLimitingQueue(workqueue.DefaultControllerRateLimiter())
	ciliumNodeInformer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc: func(obj interface{}) {
			key, err := cache.MetaNamespaceKeyFunc(obj)
			if err == nil {
				ciliumNodeQueue.Add(key)
			}
		},
	})

	// Create event broadcaster
	// Add ciliumNode types to the default Kubernetes Scheme so Events can be
	// logged for ciliumNode types.
	utilruntime.Must(versionedScheme.AddToScheme(scheme.Scheme))
	logger.V(4).Info("Creating event broadcaster")

	eventBroadcaster := record.NewBroadcaster()
	eventBroadcaster.StartStructuredLogging(0)
	eventBroadcaster.StartRecordingToSink(&v1.EventSinkImpl{Interface: clientset.CoreV1().Events("")})
	recorder := eventBroadcaster.NewRecorder(scheme.Scheme, corev1.EventSource{Component: ControllerCiliumNodeName})

	return &CiliumNodeController{
		recorder:           recorder,
		ciliumNodeInformer: ciliumNodeInformer,
		ciliumNodeQueue:    ciliumNodeQueue,
		ciliumNodeInLister: ciliumNodeInLister,
	}
}

// startInformer
// @Description: 启动informer, 和apiserver建立长连接, 从apiserver获取资源变化
// @receiver c
// @param ctx
func (c *CiliumNodeController) startClientInformer(ctx context.Context) {
	go c.ciliumNodeInformer.Run(ctx.Done())
}

// processCiliumNodeNextItem
// @Description: 从队列中取出key, 并处理key对应的资源
// @receiver c
// @param ctx
// @return bool
func (c *CiliumNodeController) processCiliumNodeNextItem(ctx context.Context) bool {
	// Wait until there is a new item in the working queue
	key, quit := c.ciliumNodeQueue.Get() //从队列中拿出key, 如果队列中没有key则阻塞, 直到有key
	logger := klog.FromContext(ctx)
	logger.Info("processNextItem")
	if quit {
		logger.Info("quit")
		return false
	}
	// Tell the queue that we are done with processing this key. This unblocks the key for other workers
	// This allows safe parallel processing because two ciliumNodes with the same key are never processed in
	// parallel.
	defer c.ciliumNodeQueue.Done(key)

	// Invoke the method containing the business logic
	err := c.syncCiliumNodeToStdout(ctx, key.(string))
	// Handle the error if something went wrong during the execution of the business logic
	c.handleErr(err, key)
	return true
}

// handleErr checks if an error happened and makes sure we will retry later.
func (c *CiliumNodeController) handleErr(err error, key interface{}) {
	if err == nil {
		// Forget about the #AddRateLimited history of the key on every successful synchronization.
		// This ensures that future processing of updates for this key is not delayed because of
		// an outdated error history.
		c.ciliumNodeQueue.Forget(key)
		return
	}

	// This controller retries 5 times if something goes wrong. After that, it stops trying.
	if c.ciliumNodeQueue.NumRequeues(key) < 5 {
		klog.Infof("Error syncing ciliumNode %v: %v", key, err)

		// Re-enqueue the key rate limited. Based on the rate limiter on the
		// queue and the re-enqueue history, the key will be processed later again.
		c.ciliumNodeQueue.AddRateLimited(key)
		return
	}

	c.ciliumNodeQueue.Forget(key)
	// Report to an external entity that, even after several retries, we could not successfully process this key
	utilruntime.HandleError(err)
	klog.Infof("Dropping ciliumNode %q out of the queue: %v", key, err)
}

// syncToStdout is the business logic of the controller. In this controller it simply prints
// information about the ciliumNode to stdout. In case an error happened, it has to simply return the error.
// The retry logic should not be part of the business logic.
func (c *CiliumNodeController) syncCiliumNodeToStdout(ctx context.Context, key string) error {
	logger := klog.LoggerWithValues(klog.FromContext(ctx), "resourceName", key)
	logger.Info("syncToStdout")

	// Get the CiliumNode resource with this namespace/name
	obj, err := c.ciliumNodeInLister.Get(key)
	if err != nil {
		if errors.IsNotFound(err) {
			utilruntime.HandleError(fmt.Errorf("ciliumNode '%s' in work queue no longer exists", key))
			return nil
		}
		return err
	}
	bytes, err := json.Marshal(obj)
	if err != nil {
		return err
	}
	var ciliumNode cilium_api_v2.CiliumNode
	if err = json.Unmarshal(bytes, &ciliumNode); err != nil {
		return err
	}

	// Note that you also have to check the uid if you have a local controlled resource, which
	// is dependent on the actual instance, to detect that a CiliumNode was recreated with the same name
	logger.Info("Sync/Add/Update for CiliumNode", "ciliumNode", ciliumNode.GetName())
	c.recorder.Event(&ciliumNode, corev1.EventTypeNormal, "Synced", "CiliumNode synced successfully")

	return nil
}

func (c *CiliumNodeController) runCiliumNodeWorker(ctx context.Context) {
	for c.processCiliumNodeNextItem(ctx) {
	}
}

// Run begins watching and syncing.
func (c *CiliumNodeController) Run(ctx context.Context, workers int) {
	defer utilruntime.HandleCrash()
	logger := klog.FromContext(ctx)
	logger.Info("Starting CiliumNode controller")

	// Wait for all involved caches to be synced, before processing items from the queue is started
	if !cache.WaitForCacheSync(ctx.Done(), c.ciliumNodeInformer.HasSynced) {
		utilruntime.HandleError(fmt.Errorf("Timed out waiting for caches to sync"))
		return
	}

	// Launch workers to process CiliumNode resources
	for i := 0; i < workers; i++ {
		go wait.UntilWithContext(ctx, c.runCiliumNodeWorker, time.Second)
	}
}
