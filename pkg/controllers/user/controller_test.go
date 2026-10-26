package user

import (
	"testing"

	klum "github.com/ibuildthecloud/klum/pkg/apis/klum.cattle.io/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestUserResources(t *testing.T) {
	h := handler{cfg: Config{Namespace: "klum", DefaultClusterRole: "view"}}
	u := &klum.User{ObjectMeta: metav1.ObjectMeta{Name: "alice"}, Spec: klum.UserSpec{
		ClusterRoles: []string{"view"}, Roles: []klum.NamespaceRole{{Namespace: "team", Role: "reader", ClusterRole: "edit"}},
	}}
	objects, _, err := h.OnUserChange(u, klum.UserStatus{})
	if err != nil {
		t.Fatal(err)
	}
	if len(objects) != 4 {
		t.Fatalf("got %d objects, want SA and three bindings", len(objects))
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
	h := handler{cfg: Config{Namespace: "klum", DefaultClusterRole: "view"}}
	u := &klum.User{ObjectMeta: metav1.ObjectMeta{Name: "alice"}}
	if roles := h.getRoles(u); len(roles) != 1 {
		t.Fatalf("expected default role, got %v", roles)
	}
	h.cfg.DefaultClusterRole = ""
	if roles := h.getRoles(u); len(roles) != 0 {
		t.Fatalf("expected no default access, got %v", roles)
	}
}
