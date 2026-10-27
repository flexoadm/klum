//go:build integration

package integration

import (
	"context"
	"encoding/json"
	"os"
	"testing"
	"time"

	klum "github.com/ibuildthecloud/klum/pkg/apis/klum.cattle.io/v1alpha1"
	"github.com/ibuildthecloud/klum/pkg/controllers/user"
	"github.com/ibuildthecloud/klum/pkg/crd"
	factory "github.com/ibuildthecloud/klum/pkg/generated/controllers/klum.cattle.io"
	"github.com/rancher/wrangler/v3/pkg/apply"
	"github.com/rancher/wrangler/v3/pkg/generated/controllers/core"
	"github.com/rancher/wrangler/v3/pkg/generated/controllers/rbac"
	"github.com/rancher/wrangler/v3/pkg/start"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
)

func TestKubeconfigIssuance(t *testing.T) {
	if os.Getenv("KLUM_DISPOSABLE_CLUSTER") != "true" {
		t.Fatal("use scripts/test-integration")
	}
	cfg, err := clientcmd.BuildConfigFromFlags("", os.Getenv("KUBECONFIG"))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	if err = crd.Create(ctx, cfg); err != nil {
		t.Fatal(err)
	}
	c, err := core.NewFactoryFromConfig(cfg)
	if err != nil {
		t.Fatal(err)
	}
	k, err := factory.NewFactoryFromConfig(cfg)
	if err != nil {
		t.Fatal(err)
	}
	r, err := rbac.NewFactoryFromConfig(cfg)
	if err != nil {
		t.Fatal(err)
	}
	a, err := apply.NewForConfig(cfg)
	if err != nil {
		t.Fatal(err)
	}
	admin, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		t.Fatal(err)
	}
	const ns = "klum-issuance"
	if _, err = admin.CoreV1().Namespaces().Create(ctx, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: ns}}, metav1.CreateOptions{}); err != nil {
		t.Fatal(err)
	}
	users := k.Klum().V1alpha1().User()
	configs := k.Klum().V1alpha1().Kubeconfig()
	user.Register(ctx, user.Config{Namespace: ns, ContextName: "test", Server: cfg.Host, DefaultClusterRole: "view"}, a, c.Core().V1().ServiceAccount(), r.Rbac().V1().ClusterRoleBinding(), r.Rbac().V1().RoleBinding(), c.Core().V1().Secret(), configs, users)
	if err = start.All(ctx, 2, k, c, r); err != nil {
		t.Fatal(err)
	}
	if _, err = users.Create(&klum.User{ObjectMeta: metav1.ObjectMeta{Name: "issuance-test"}}); err != nil {
		t.Fatal(err)
	}
	var issued *klum.Kubeconfig
	poll := func(check func() (bool, error)) {
		t.Helper()
		if err := wait.PollUntilContextCancel(ctx, 250*time.Millisecond, true, func(context.Context) (bool, error) { return check() }); err != nil {
			t.Fatal(err)
		}
	}
	poll(func() (bool, error) {
		var e error
		issued, e = configs.Get("issuance-test", metav1.GetOptions{})
		if apierrors.IsNotFound(e) {
			return false, nil
		}
		return e == nil && len(issued.Spec.AuthInfos) > 0 && issued.Spec.AuthInfos[0].AuthInfo.Token != "", e
	})
	if len(issued.OwnerReferences) != 1 || issued.OwnerReferences[0].Kind != "User" {
		t.Fatalf("invalid owner: %v", issued.OwnerReferences)
	}
	raw, err := json.Marshal(issued.Spec)
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := clientcmd.NewClientConfigFromBytes(raw)
	if err != nil {
		t.Fatal(err)
	}
	access, err := parsed.ClientConfig()
	if err != nil {
		t.Fatal(err)
	}
	client, err := kubernetes.NewForConfig(access)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = client.CoreV1().Pods("default").List(ctx, metav1.ListOptions{}); err != nil {
		t.Fatalf("issued kubeconfig cannot access API: %v", err)
	}
	original := issued.Spec.AuthInfos[0].AuthInfo.Token
	// A no-op user update must preserve the token.
	u, err := users.Get("issuance-test", metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	u.Annotations = map[string]string{"test": "reconcile"}
	if _, err = users.Update(u); err != nil {
		t.Fatal(err)
	}
	disabled := false
	poll(func() (bool, error) {
		u, e := users.Get("issuance-test", metav1.GetOptions{})
		if e != nil {
			return false, e
		}
		if !klum.UserReadyCondition.IsTrue(u) {
			return false, nil
		}
		u.Spec.Enabled = &disabled
		_, e = users.Update(u)
		if apierrors.IsConflict(e) {
			return false, nil
		}
		return e == nil, e
	})
	poll(func() (bool, error) {
		_, e := configs.Get("issuance-test", metav1.GetOptions{})
		return apierrors.IsNotFound(e), nil
	})
	poll(func() (bool, error) {
		_, e := admin.CoreV1().Secrets(ns).Get(ctx, "klum-issuance-test-token", metav1.GetOptions{})
		return apierrors.IsNotFound(e), nil
	})
	enabled := true
	poll(func() (bool, error) {
		u, e := users.Get("issuance-test", metav1.GetOptions{})
		if e != nil {
			return false, e
		}
		u.Spec.Enabled = &enabled
		_, e = users.Update(u)
		if apierrors.IsConflict(e) {
			return false, nil
		}
		return e == nil, e
	})
	poll(func() (bool, error) {
		kc, e := configs.Get("issuance-test", metav1.GetOptions{})
		if apierrors.IsNotFound(e) {
			return false, nil
		}
		return e == nil && kc.Spec.AuthInfos[0].AuthInfo.Token != "" && kc.Spec.AuthInfos[0].AuthInfo.Token != original, e
	})
	if err = users.Delete("issuance-test", &metav1.DeleteOptions{}); err != nil {
		t.Fatal(err)
	}
	poll(func() (bool, error) {
		_, e := configs.Get("issuance-test", metav1.GetOptions{})
		return apierrors.IsNotFound(e), nil
	})
}
