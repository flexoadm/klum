package user

import (
	"fmt"
	controllers "github.com/ibuildthecloud/klum/pkg/generated/controllers/klum.cattle.io/v1alpha1"
	corecontroller "github.com/rancher/wrangler/v3/pkg/generated/controllers/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"testing"
	"time"

	klum "github.com/ibuildthecloud/klum/pkg/apis/klum.cattle.io/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestUserResources(t *testing.T) {
	h := newTestHandler()
	u := &klum.User{ObjectMeta: metav1.ObjectMeta{Name: "alice"}, Spec: klum.UserSpec{
		ClusterRoles: []string{"view"}, Roles: []klum.NamespaceRole{{Namespace: "team", Role: "reader", ClusterRole: "edit"}},
	}}
	objects, _, err := h.OnUserChange(u, klum.UserStatus{})
	if err != nil {
		t.Fatal(err)
	}
	if len(objects) != 5 {
		t.Fatalf("got %d objects, want SA, three bindings and token Secret", len(objects))
	}
	sa, ok := objects[0].(*corev1.ServiceAccount)
	if !ok || sa.Name != "alice" || sa.Namespace != "klum" {
		t.Fatalf("unexpected service account: %#v", objects[0])
	}
	crb := objects[1].(*rbacv1.ClusterRoleBinding)
	if crb.RoleRef.Name != "view" || crb.Subjects[0].Name != sa.Name || crb.Subjects[0].Namespace != sa.Namespace {
		t.Fatalf("incorrect cluster binding: %#v", crb)
	}
	for i, kind := range []string{"Role", "ClusterRole"} {
		rb := objects[i+2].(*rbacv1.RoleBinding)
		if rb.Namespace != "team" || rb.RoleRef.Kind != kind || rb.Subjects[0].Namespace != "klum" {
			t.Fatalf("incorrect namespace binding: %#v", rb)
		}
	}
	disabled := false
	u.Spec.Enabled = &disabled
	objects, _, err = h.OnUserChange(u, klum.UserStatus{})
	if err != nil || len(objects) != 0 {
		t.Fatalf("disabled user still generates resources: %v, %v", objects, err)
	}
}

func TestDefaultRoleCanBeDisabled(t *testing.T) {
	h := newTestHandler()
	u := &klum.User{ObjectMeta: metav1.ObjectMeta{Name: "alice"}}
	if roles := h.getRoles(u); len(roles) != 1 {
		t.Fatalf("expected default role, got %v", roles)
	}
	h.cfg.DefaultClusterRole = ""
	if roles := h.getRoles(u); len(roles) != 0 {
		t.Fatalf("expected no default access, got %v", roles)
	}
}

type saCache struct {
	corecontroller.ServiceAccountCache
	sa *corev1.ServiceAccount
}

func (c saCache) Get(namespace, name string) (*corev1.ServiceAccount, error) {
	if c.sa == nil {
		return nil, apierrors.NewNotFound(schema.GroupResource{Resource: "serviceaccounts"}, name)
	}
	return c.sa, nil
}

type secretCache struct {
	corecontroller.SecretCache
	objects map[string]*corev1.Secret
}

func (c secretCache) Get(namespace, name string) (*corev1.Secret, error) {
	if s := c.objects[name]; s != nil {
		return s, nil
	}
	return nil, apierrors.NewNotFound(schema.GroupResource{Resource: "secrets"}, name)
}

type userQueue struct {
	controllers.UserController
	queued []string
}

func (q *userQueue) Enqueue(name string)                       { q.queued = append(q.queued, name) }
func (q *userQueue) EnqueueAfter(name string, _ time.Duration) { q.Enqueue(name) }
func newTestHandler() handler {
	return handler{cfg: Config{Namespace: "klum", DefaultClusterRole: "view", ContextName: "test", Server: "https://example.test"}, serviceAccounts: saCache{}, secrets: secretCache{objects: map[string]*corev1.Secret{}}, users: &userQueue{}}
}
func TestTokenLifecycle(t *testing.T) {
	h := newTestHandler()
	u := &klum.User{ObjectMeta: metav1.ObjectMeta{Name: "alice"}}
	objects, status, err := h.OnUserChange(u, klum.UserStatus{})
	if err != nil {
		t.Fatal(err)
	}
	secret := objects[len(objects)-1].(*corev1.Secret)
	if secret.Type != corev1.SecretTypeServiceAccountToken || secret.Annotations[corev1.ServiceAccountNameKey] != "alice" || len(secret.Data) != 0 {
		t.Fatalf("bad token request: %#v", secret)
	}
	if klum.UserReadyCondition.IsTrue(&klum.User{Status: status}) {
		t.Fatal("ready before token exists")
	}
	sa := objects[0].(*corev1.ServiceAccount)
	sa.UID = "current"
	h.serviceAccounts = saCache{sa: sa}
	secret = secret.DeepCopy()
	secret.Annotations[corev1.ServiceAccountUIDKey] = "current"
	h.secrets = secretCache{objects: map[string]*corev1.Secret{secret.Name: secret}}
	for _, token := range []string{"", "issued-token"} {
		secret.Data = map[string][]byte{corev1.ServiceAccountTokenKey: []byte(token), corev1.ServiceAccountRootCAKey: []byte("CA")}
		objects, status, err = h.OnUserChange(u, status)
		if err != nil {
			t.Fatal(err)
		}
		ready := klum.UserReadyCondition.IsTrue(&klum.User{Status: status})
		if ready != (token != "") {
			t.Fatalf("token %q: Ready=%v", token, ready)
		}
		if ready {
			kc := objects[len(objects)-1].(*klum.Kubeconfig)
			if kc.Spec.AuthInfos[0].AuthInfo.Token != token || kc.Spec.Clusters[0].Cluster.CertificateAuthorityData != "Q0E=" {
				t.Fatalf("invalid kubeconfig: %#v", kc.Spec)
			}
		}
	}
	for i := 0; i < 2; i++ {
		if _, _, err := h.OnUserChange(u, status); err != nil {
			t.Fatal(err)
		}
	}
	secret.Annotations[corev1.ServiceAccountUIDKey] = "old"
	_, status, err = h.OnUserChange(u, status)
	if err != nil || klum.UserReadyCondition.IsTrue(&klum.User{Status: status}) {
		t.Fatal("accepted old SA token", err)
	}
	disabled := false
	u.Spec.Enabled = &disabled
	objects, _, err = h.OnUserChange(u, status)
	if err != nil || len(objects) != 0 {
		t.Fatal("disabled resources remain", err)
	}
}
func TestLegacyTokenPreserved(t *testing.T) {
	h := newTestHandler()
	sa := &corev1.ServiceAccount{ObjectMeta: metav1.ObjectMeta{Name: "alice", Namespace: "klum", UID: "current"}, Secrets: []corev1.ObjectReference{{Name: "legacy"}}}
	secret := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "legacy", Namespace: "klum", Annotations: map[string]string{corev1.ServiceAccountNameKey: "alice", corev1.ServiceAccountUIDKey: "current"}}, Type: corev1.SecretTypeServiceAccountToken, Data: map[string][]byte{corev1.ServiceAccountTokenKey: []byte("existing-token")}}
	h.serviceAccounts = saCache{sa: sa}
	h.secrets = secretCache{objects: map[string]*corev1.Secret{"legacy": secret}}
	objects, _, err := h.OnUserChange(&klum.User{ObjectMeta: metav1.ObjectMeta{Name: "alice"}}, klum.UserStatus{})
	if err != nil {
		t.Fatal(err)
	}
	for _, o := range objects {
		if _, ok := o.(*corev1.Secret); ok {
			t.Fatal("created replacement for legacy token")
		}
	}
	kc := objects[len(objects)-1].(*klum.Kubeconfig)
	if kc.Spec.AuthInfos[0].AuthInfo.Token != "existing-token" {
		t.Fatal("legacy token changed")
	}
}
func TestSecretEventsAndCollision(t *testing.T) {
	h := newTestHandler()
	sa := &corev1.ServiceAccount{ObjectMeta: metav1.ObjectMeta{Name: "alice", Namespace: "klum", Annotations: map[string]string{"klum.cattle.io/user": "alice"}}}
	h.serviceAccounts = saCache{sa: sa}
	secret := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "klum-alice-token", Namespace: "klum", Annotations: map[string]string{corev1.ServiceAccountNameKey: "alice"}}, Type: corev1.SecretTypeServiceAccountToken}
	if _, err := h.OnSecretChange("", secret); err != nil {
		t.Fatal(err)
	}
	if got := h.users.(*userQueue).queued; fmt.Sprint(got) != "[alice]" {
		t.Fatalf("not requeued: %v", got)
	}
	secret.Annotations[corev1.ServiceAccountNameKey] = "someone-else"
	h.secrets = secretCache{objects: map[string]*corev1.Secret{secret.Name: secret}}
	if _, _, err := h.OnUserChange(&klum.User{ObjectMeta: metav1.ObjectMeta{Name: "alice"}}, klum.UserStatus{}); err == nil {
		t.Fatal("overwrites unrelated Secret")
	}
}

func TestManagedSecretReferenceIsRetained(t *testing.T) {
	h := newTestHandler()
	sa := &corev1.ServiceAccount{ObjectMeta: metav1.ObjectMeta{Name: "alice", Namespace: "klum", UID: "current"}, Secrets: []corev1.ObjectReference{{Name: "klum-alice-token"}}}
	secret := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "klum-alice-token", Namespace: "klum", Annotations: map[string]string{corev1.ServiceAccountNameKey: "alice", corev1.ServiceAccountUIDKey: "current"}}, Type: corev1.SecretTypeServiceAccountToken, Data: map[string][]byte{corev1.ServiceAccountTokenKey: []byte("existing-token")}}
	h.serviceAccounts = saCache{sa: sa}
	h.secrets = secretCache{objects: map[string]*corev1.Secret{secret.Name: secret}}
	objects, _, err := h.OnUserChange(&klum.User{ObjectMeta: metav1.ObjectMeta{Name: "alice"}}, klum.UserStatus{})
	if err != nil {
		t.Fatal(err)
	}
	for _, o := range objects {
		if s, ok := o.(*corev1.Secret); ok && s.Name == secret.Name {
			return
		}
	}
	t.Fatal("referenced managed Secret would be pruned")
}
