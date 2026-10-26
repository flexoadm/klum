//go:build integration

package integration

import (
	"context"
	"os"
	"testing"
	"time"

	klum "github.com/ibuildthecloud/klum/pkg/apis/klum.cattle.io/v1alpha1"
	"github.com/ibuildthecloud/klum/pkg/crd"
	klumclient "github.com/ibuildthecloud/klum/pkg/generated/clientset/versioned"
	"github.com/rancher/wrangler/v3/pkg/genericcondition"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
)

// Run only against the disposable cluster created by scripts/test-integration.
func TestModernAPIs(t *testing.T) {
	if os.Getenv("KLUM_DISPOSABLE_CLUSTER") != "true" {
		t.Fatal("use scripts/test-integration to create an isolated cluster")
	}
	cfg, err := clientcmd.BuildConfigFromFlags("", os.Getenv("KUBECONFIG"))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	if err := crd.Create(ctx, cfg); err != nil {
		t.Fatal(err)
	}
	// A second application exercises updates to existing v1 CRDs.
	if err := crd.Create(ctx, cfg); err != nil {
		t.Fatal(err)
	}
	client, err := klumclient.NewForConfig(cfg)
	if err != nil {
		t.Fatal(err)
	}
	users := client.KlumV1alpha1().Users()
	enabled := true
	u, err := users.Create(ctx, &klum.User{ObjectMeta: metav1.ObjectMeta{Name: "api-test"}, Spec: klum.UserSpec{Enabled: &enabled, ClusterRoles: []string{"view"}, Roles: []klum.NamespaceRole{{Namespace: "default", Role: "reader"}}}}, metav1.CreateOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if u.Spec.Enabled == nil || !*u.Spec.Enabled || len(u.Spec.Roles) != 1 {
		t.Fatalf("fields lost during round trip: %#v", u.Spec)
	}
	u.Status.Conditions = []genericcondition.GenericCondition{{Type: "Ready", Status: corev1.ConditionTrue}}
	u, err = users.UpdateStatus(ctx, u, metav1.UpdateOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(u.Status.Conditions) != 1 {
		t.Fatal("status was pruned")
	}
	if _, err := users.List(ctx, metav1.ListOptions{}); err != nil {
		t.Fatal(err)
	}
	kc := &klum.Kubeconfig{ObjectMeta: metav1.ObjectMeta{Name: "api-test"}, Spec: klum.KubeconfigSpec{
		Clusters:  []klum.NamedCluster{{Name: "test", Cluster: klum.Cluster{Server: "https://example.test", CertificateAuthorityData: "Y2E="}}},
		AuthInfos: []klum.NamedAuthInfo{{Name: "test", AuthInfo: klum.AuthInfo{Token: "fixture-token"}}},
		Contexts:  []klum.NamedContext{{Name: "test", Context: klum.Context{Cluster: "test", AuthInfo: "test"}}}, CurrentContext: "test",
	}}
	saved, err := client.KlumV1alpha1().Kubeconfigs().Create(ctx, kc, metav1.CreateOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if saved.Spec.AuthInfos[0].AuthInfo.Token != "fixture-token" || saved.Spec.CurrentContext != "test" {
		t.Fatal("kubeconfig fields were pruned")
	}
	core, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := core.CoreV1().ServiceAccounts("default").Create(ctx, &corev1.ServiceAccount{ObjectMeta: metav1.ObjectMeta{Name: "api-test"}}, metav1.CreateOptions{}); err != nil {
		t.Fatal(err)
	}
	if _, err := core.CoreV1().Secrets("default").Create(ctx, &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "api-test"}, Type: corev1.SecretTypeOpaque, StringData: map[string]string{"fixture": "test"}}, metav1.CreateOptions{}); err != nil {
		t.Fatal(err)
	}
	if _, err := core.RbacV1().RoleBindings("default").Create(ctx, &rbacv1.RoleBinding{ObjectMeta: metav1.ObjectMeta{Name: "api-test"}, Subjects: []rbacv1.Subject{{Kind: "ServiceAccount", Name: "api-test", Namespace: "default"}}, RoleRef: rbacv1.RoleRef{APIGroup: "rbac.authorization.k8s.io", Kind: "ClusterRole", Name: "view"}}, metav1.CreateOptions{}); err != nil {
		t.Fatal(err)
	}
	if err := users.Delete(ctx, "api-test", metav1.DeleteOptions{}); err != nil {
		t.Fatal(err)
	}
	if _, err := users.Get(ctx, "api-test", metav1.GetOptions{}); !apierrors.IsNotFound(err) {
		t.Fatalf("expected user deletion, got %v", err)
	}
}
