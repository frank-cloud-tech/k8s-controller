# k8s-controller 快速开发指南

## 手写controller

> 不依赖kubebuilder等controller框架

### 无crd,调谐原生资源
[controller.go](handwriting%2Fno-crd%2Fcontroller.go)

### 有crd,调谐自定义资源
1. 构思crd的自定义字段, 准备好crd的yaml文件 [crd.yaml](handwriting%2Fwith-crd%2Fartifacts%2Fcrd.yaml)
   - 官方文档: https://kubernetes.io/zh-cn/docs/tasks/extend-kubernetes/custom-resources/custom-resource-definitions/

2. 根据准备好的crd文件, 写对应的结构体等必要文件 [foo.go](handwriting%2Fwith-crd%2Fapis%2Ffoo%2Fv1alpha1%2Ffoo.go),  [doc.go](handwriting%2Fwith-crd%2Fapis%2Ffoo%2Fv1alpha1%2Fdoc.go), [register.go](handwriting%2Fwith-crd%2Fapis%2Ffoo%2Fv1alpha1%2Fregister.go)
   - 前两个文件中的注释不要删除

3. 然后生成代码

   ```bash
   go mod vendor
   chmod +x ../vendor/k8s.io/code-generator/generate-groups.sh
   chmod +x ../vendor/k8s.io/code-generator/generate-internal-groups.sh
   cd hack
   # update-codegen.sh 的内容视情况修改
   ./update-codegen.sh
   ```
   执行完之后会生成一个文件和一个目录: [zz_generated.deepcopy.go](handwriting%2Fwith-crd%2Fapis%2Ffoo%2Fv1alpha1%2Fzz_generated.deepcopy.go), [generated](handwriting%2Fwith-crd%2Fgenerated), 这些内容不要手动修改.

4. 实现controller
    - [controller.go](handwriting%2Fwith-crd%2Fcontroller.go)

### 有crd, 调谐自定义资源和原生资源

### 有crd, 调谐自定义资源,原生资源和第三方自定义资源

