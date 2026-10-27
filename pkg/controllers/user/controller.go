package user

import (
	"context"
	"crypto/md5"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"time"

	klum "github.com/ibuildthecloud/klum/pkg/apis/klum.cattle.io/v1alpha1"
	"github.com/ibuildthecloud/klum/pkg/generated/controllers/klum.cattle.io/v1alpha1"
	"github.com/rancher/wrangler/v3/pkg/apply"
	v1controller "github.com/rancher/wrangler/v3/pkg/generated/controllers/core/v1"
	rbaccontroller "github.com/rancher/wrangler/v3/pkg/generated/controllers/rbac/v1"
	"github.com/rancher/wrangler/v3/pkg/generic"
	name2 "github.com/rancher/wrangler/v3/pkg/name"
	v1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

type Config struct {
	Namespace          string
	ContextName        string
	Server             string
	CA                 string
	DefaultClusterRole string
}

func Register(ctx context.Context,
	cfg Config,
	apply apply.Apply,
	serviceAccount v1controller.ServiceAccountController,
	crb rbaccontroller.ClusterRoleBindingController,
	rb rbaccontroller.RoleBindingController,
	secrets v1controller.SecretController,
	kconfig v1alpha1.KubeconfigController,
	user v1alpha1.UserController) {

	h := &handler{
		cfg:             cfg,
		secrets:         secrets.Cache(),
		users:           user,
		serviceAccounts: serviceAccount.Cache(),
	}

	v1alpha1.RegisterUserGeneratingHandler(ctx,
		user,
		apply.WithCacheTypes(serviceAccount,
			crb, rb, secrets, kconfig).WithSetOwnerReference(true, false),
		"",
		"klum-user",
		h.OnUserChange,
		&generic.GeneratingHandlerOptions{
			AllowClusterScoped: true,
		})

	secrets.OnChange(ctx, "klum-secret", h.OnSecretChange)
	secrets.OnRemove(ctx, "klum-secret-remove", h.OnSecretChange)
	serviceAccount.OnChange(ctx, "klum-service-account", h.OnServiceAccountChange)
}

type handler struct {
	cfg             Config
	serviceAccounts v1controller.ServiceAccountCache
	secrets         v1controller.SecretCache
	users           v1alpha1.UserController
}

func (h *handler) OnUserChange(user *klum.User, status klum.UserStatus) ([]runtime.Object, klum.UserStatus, error) {
	if user.Spec.Enabled != nil && !*user.Spec.Enabled {
		status = setReady(status, false)
		return nil, status, nil
	}

	objs := []runtime.Object{
		&v1.ServiceAccount{
			ObjectMeta: metav1.ObjectMeta{
				Name:      user.Name,
				Namespace: h.cfg.Namespace,
				Annotations: map[string]string{
					"klum.cattle.io/user": user.Name,
				},
			},
		},
	}
	objs = append(objs, h.getRoles(user)...)

	// Prefer a previously issued token referenced by the SA to preserve existing access.
	sa, err := h.serviceAccounts.Get(h.cfg.Namespace, user.Name)
	if err != nil && !errors.IsNotFound(err) {
		return nil, status, err
	}
	var tokenSecret *v1.Secret
	if sa != nil {
		for _, ref := range sa.Secrets {
			candidate, err := h.secrets.Get(sa.Namespace, ref.Name)
			if errors.IsNotFound(err) {
				continue
			}
			if err != nil {
				return nil, status, err
			}
			if validToken(candidate, sa) {
				tokenSecret = candidate
				break
			}
		}
	}
	secretName := name2.SafeConcatName("klum", user.Name, "token")
	if tokenSecret == nil || tokenSecret.Name == secretName {
		desired := &v1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:        secretName,
				Namespace:   h.cfg.Namespace,
				Annotations: map[string]string{v1.ServiceAccountNameKey: user.Name},
			},
			Type: v1.SecretTypeServiceAccountToken,
		}
		// Do not specify data: the Kubernetes token controller fills it asynchronously.
		objs = append(objs, desired)
		candidate, err := h.secrets.Get(h.cfg.Namespace, secretName)
		if err != nil && !errors.IsNotFound(err) {
			return nil, status, err
		}
		if candidate != nil && (candidate.Type != desired.Type || candidate.Annotations[v1.ServiceAccountNameKey] != user.Name) {
			return nil, status, fmt.Errorf("token Secret %s/%s belongs to another account or has an unexpected type", h.cfg.Namespace, secretName)
		}
		if sa != nil && validToken(candidate, sa) {
			tokenSecret = candidate
		}
	}
	if tokenSecret == nil {
		if h.users != nil {
			h.users.EnqueueAfter(user.Name, 5*time.Second)
		}
		status = setReady(status, false)
		u := &klum.User{Status: status}
		klum.UserReadyCondition.Reason(u, "WaitingForToken")
		klum.UserReadyCondition.Message(u, "Waiting for Kubernetes to populate the ServiceAccount token Secret")
		return objs, u.Status, nil
	}
	objs = append(objs, h.kubeconfig(user.Name, tokenSecret))
	status = setReady(status, true)
	u := &klum.User{Status: status}
	klum.UserReadyCondition.Reason(u, "")
	klum.UserReadyCondition.Message(u, "")
	return objs, u.Status, nil
}

func (h *handler) getRoles(user *klum.User) []runtime.Object {
	subjects := []rbacv1.Subject{
		{
			Kind:      "ServiceAccount",
			Name:      user.Name,
			Namespace: h.cfg.Namespace,
		},
	}

	if len(user.Spec.ClusterRoles) == 0 && len(user.Spec.Roles) == 0 {
		if h.cfg.DefaultClusterRole == "" {
			return nil
		}
		return []runtime.Object{
			&rbacv1.ClusterRoleBinding{
				ObjectMeta: metav1.ObjectMeta{
					Name: name(user.Name, "", h.cfg.DefaultClusterRole, ""),
				},
				Subjects: subjects,
				RoleRef: rbacv1.RoleRef{
					APIGroup: "rbac.authorization.k8s.io",
					Kind:     "ClusterRole",
					Name:     h.cfg.DefaultClusterRole,
				},
			},
		}
	}

	var objs []runtime.Object

	for _, clusterRole := range user.Spec.ClusterRoles {
		objs = append(objs, &rbacv1.ClusterRoleBinding{
			ObjectMeta: metav1.ObjectMeta{
				Name: name(user.Name, "", clusterRole, ""),
			},
			Subjects: subjects,
			RoleRef: rbacv1.RoleRef{
				APIGroup: "rbac.authorization.k8s.io",
				Kind:     "ClusterRole",
				Name:     clusterRole,
			},
		})
	}

	for _, role := range user.Spec.Roles {
		if role.Namespace == "" ||
			role.Role == "" && role.ClusterRole == "" {
			continue
		}

		if role.Role != "" {
			rb := &rbacv1.RoleBinding{
				ObjectMeta: metav1.ObjectMeta{
					Name:      name(user.Name, role.Namespace, "", role.Role),
					Namespace: role.Namespace,
				},
				Subjects: subjects,
				RoleRef: rbacv1.RoleRef{
					APIGroup: "rbac.authorization.k8s.io",
					Kind:     "Role",
					Name:     role.Role,
				},
			}
			objs = append(objs, rb)
		}

		if role.ClusterRole != "" {
			rb := &rbacv1.RoleBinding{
				ObjectMeta: metav1.ObjectMeta{
					Name:      name(user.Name, role.Namespace, role.ClusterRole, ""),
					Namespace: role.Namespace,
				},
				Subjects: subjects,
				RoleRef: rbacv1.RoleRef{
					APIGroup: "rbac.authorization.k8s.io",
					Kind:     "ClusterRole",
					Name:     role.ClusterRole,
				},
			}
			objs = append(objs, rb)
		}
	}

	return objs
}

func name(user, namespace, clusterRole, role string) string {
	// this is so we don't get conflicts
	suffix := md5.Sum([]byte(fmt.Sprintf("%s/%s/%s/%s", user, namespace, clusterRole, role)))
	if role == "" {
		role = clusterRole
	}
	return name2.SafeConcatName("klum", user, role, hex.EncodeToString(suffix[:])[:8])
}

// validToken rejects empty tokens and tokens bound to an older incarnation of the SA.
func validToken(secret *v1.Secret, sa *v1.ServiceAccount) bool {
	return secret != nil && sa.UID != "" && secret.Type == v1.SecretTypeServiceAccountToken &&
		secret.Annotations[v1.ServiceAccountNameKey] == sa.Name &&
		secret.Annotations[v1.ServiceAccountUIDKey] == string(sa.UID) && len(secret.Data[v1.ServiceAccountTokenKey]) > 0
}

func (h *handler) OnServiceAccountChange(key string, sa *v1.ServiceAccount) (*v1.ServiceAccount, error) {
	if sa != nil && sa.Namespace == h.cfg.Namespace {
		if userName := sa.Annotations["klum.cattle.io/user"]; userName != "" {
			h.users.Enqueue(userName)
		}
	}
	return sa, nil
}

func (h *handler) OnSecretChange(key string, secret *v1.Secret) (*v1.Secret, error) {
	if secret == nil || secret.Namespace != h.cfg.Namespace || secret.Type != v1.SecretTypeServiceAccountToken {
		return secret, nil
	}
	sa, err := h.serviceAccounts.Get(secret.Namespace, secret.Annotations[v1.ServiceAccountNameKey])
	if errors.IsNotFound(err) {
		return secret, nil
	}
	if err != nil {
		return secret, err
	}
	if userName := sa.Annotations["klum.cattle.io/user"]; userName != "" {
		h.users.Enqueue(userName)
	}
	return secret, nil
}

func (h *handler) kubeconfig(userName string, secret *v1.Secret) *klum.Kubeconfig {
	ca := h.cfg.CA
	if ca == "" {
		ca = base64.StdEncoding.EncodeToString(secret.Data["ca.crt"])
	}
	token := string(secret.Data["token"])

	return &klum.Kubeconfig{
		ObjectMeta: metav1.ObjectMeta{
			Name: userName,
		},
		Spec: klum.KubeconfigSpec{
			Clusters: []klum.NamedCluster{
				{
					Name: h.cfg.ContextName,
					Cluster: klum.Cluster{
						Server:                   h.cfg.Server,
						CertificateAuthorityData: ca,
					},
				},
			},
			AuthInfos: []klum.NamedAuthInfo{
				{
					Name: h.cfg.ContextName,
					AuthInfo: klum.AuthInfo{
						Token: token,
					},
				},
			},
			Contexts: []klum.NamedContext{
				{
					Name: h.cfg.ContextName,
					Context: klum.Context{
						Cluster:  h.cfg.ContextName,
						AuthInfo: h.cfg.ContextName,
					},
				},
			},
			CurrentContext: h.cfg.ContextName,
		},
	}
}

func setReady(status klum.UserStatus, ready bool) klum.UserStatus {
	// dumb hack to set condition, should really make this easier
	user := &klum.User{Status: status}
	klum.UserReadyCondition.SetStatusBool(user, ready)
	return user.Status
}
