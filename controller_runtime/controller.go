// Package main
// @Description:
package main

import (
	"context"
	"fmt"
	api "k8s-controller/handwriting/with_crd/apis/foo/v1alpha1"
	v1 "k8s.io/api/apps/v1"
	_ "k8s.io/client-go/plugin/pkg/client/auth/gcp"

	"os"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
)

var (
	setupLog = ctrl.Log.WithName("setup")
)

type reconciler struct {
	client.Client
}

type reconcilerDeploy struct {
	client.Client
}

func (r *reconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := log.FromContext(ctx).WithValues("foo", req.NamespacedName)
	log.V(1).Info("reconciling foo")

	var foo api.Foo
	if err := r.Get(ctx, req.NamespacedName, &foo); err != nil {
		log.Error(err, "unable to get foo")
		return ctrl.Result{}, err
	}

	fmt.Printf("Sync/Add/Update for foo %s\n", foo.GetName())
	return ctrl.Result{}, nil
}

func (r *reconcilerDeploy) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := log.FromContext(ctx).WithValues("deploy", req.NamespacedName)
	log.V(1).Info("reconciling deploy")

	var deploy v1.Deployment
	if err := r.Get(ctx, req.NamespacedName, &deploy); err != nil {
		log.Error(err, "unable to get deploy")
		return ctrl.Result{}, err
	}

	fmt.Printf("Sync/Add/Update for deploy %s\n", deploy.GetName())
	return ctrl.Result{}, nil
}

func main() {
	ctrl.SetLogger(zap.New())

	//Manager 可以管理多个Controller 统一管理这些 Controller 的生命周期
	mgr, err := ctrl.NewManager(ctrl.GetConfigOrDie(), ctrl.Options{
		LeaderElection:          true,
		LeaderElectionID:        "foo-controller",
		LeaderElectionNamespace: "default",
	})
	if err != nil {
		setupLog.Error(err, "unable to start manager")
		os.Exit(1)
	}

	// in a real controller, we'd create a new scheme for this
	err = api.AddToScheme(mgr.GetScheme())
	if err != nil {
		setupLog.Error(err, "unable to add scheme")
		os.Exit(1)
	}

	//向Manager中注册自定义资源foo的controller
	err = ctrl.NewControllerManagedBy(mgr).
		For(&api.Foo{}).
		Complete(&reconciler{
			Client: mgr.GetClient(),
		})
	if err != nil {
		setupLog.Error(err, "unable to create controller")
		os.Exit(1)
	}

	//向Manager中注册原生资源deployment的controller
	err = ctrl.NewControllerManagedBy(mgr).
		For(&v1.Deployment{}).
		Complete(&reconcilerDeploy{
			Client: mgr.GetClient(),
		})
	if err != nil {
		setupLog.Error(err, "unable to create controller")
		os.Exit(1)
	}

	//如果资源实现了Validator 和 Defaulter 两个 Interface 中的相关方法, 会自动创建webhook,path的命名规则如下:
	//func generateMutatePath(gvk schema.GroupVersionKind) string {
	//return "/mutate-" + strings.ReplaceAll(gvk.Group, ".", "-") + "-" +
	//	gvk.Version + "-" + strings.ToLower(gvk.Kind)
	//}
	//
	//func generateValidatePath(gvk schema.GroupVersionKind) string {
	//	return "/validate-" + strings.ReplaceAll(gvk.Group, ".", "-") + "-" +
	//		gvk.Version + "-" + strings.ToLower(gvk.Kind)
	//}
	// /validate-frank-com-v1alpha1-foo
	// /mutate-frank-com-v1alpha1-foo
	//err = ctrl.NewWebhookManagedBy(mgr).
	//	For(&api.Foo{}).
	//	Complete()
	//if err != nil {
	//	setupLog.Error(err, "unable to create webhook")
	//	os.Exit(1)
	//}

	setupLog.Info("starting manager")
	if err := mgr.Start(ctrl.SetupSignalHandler()); err != nil {
		setupLog.Error(err, "problem running manager")
		os.Exit(1)
	}
}
