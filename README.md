# k8s-controller 快速开发指南

## 手写controller

> 不依赖kubebuilder等controller框架

### 无crd,调谐原生资源
[controller.go](handwriting%2Fno-crd%2Fcontroller.go)

### 有crd,调谐自定义资源
构思crd的自定义字段, 准备好crd的yaml文件 [crd.yaml](handwriting%2Fwith-crd%2Fartifacts%2Fcrd.yaml)
  - 如何定义: https://kubernetes.io/zh-cn/docs/tasks/extend-kubernetes/custom-resources/custom-resource-definitions/

根据准备好的crd文件, 写对应的结构体 [foo.go](handwriting%2Fwith-crd%2Fapis%2Ffoo%2Fv1alpha1%2Ffoo.go) [doc.go](handwriting%2Fwith-crd%2Fapis%2Ffoo%2Fv1alpha1%2Fdoc.go)
  - 这两个文件中的注释不要删除

然后生成代码:
```shell

```


### 有crd和, 调谐自定义资源和原生资源