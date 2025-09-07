package controller

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"strings"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	meta "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"github.com/go-logr/logr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	crlog "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
)

type Options struct {
	LabelSelector         string
	SecretKey             string
	SecretPrefix          string
	LoftDomain            string
	ServerTemplate        string
	CASecretNS            string
	CASecretName          string
	CASecretKey           string
	FluxNamespacePatterns []string
	ControllerNamespace   string
}

type VciReconciler struct {
	client.Client
	Log  logr.Logger
	Opts Options
}

func NewVciReconciler(c client.Client, log logr.Logger, opts Options) *VciReconciler {
	return &VciReconciler{Client: c, Log: log, Opts: opts}
}

var (
	gvkVCI = schema.GroupVersionKind{
		Group:   "management.loft.sh",
		Version: "v1",
		Kind:    "VirtualClusterInstance",
	}
	gvkAK = schema.GroupVersionKind{
		Group:   "storage.loft.sh",
		Version: "v1",
		Kind:    "AccessKey",
	}
)

func (r *VciReconciler) SetupWithManager(mgr ctrl.Manager) error {
	u := &unstructured.Unstructured{}
	u.SetGroupVersionKind(gvkVCI)

	// Label-based filtering predicate
	pred := predicate.NewPredicateFuncs(func(o client.Object) bool {
		if o == nil {
			return false
		}
		if r.Opts.LabelSelector == "" {
			return true
		}
		sel, err := labels.Parse(r.Opts.LabelSelector)
		if err != nil {
			return true // if bad selector, don't block events
		}
		return sel.Matches(labels.Set(o.GetLabels()))
	})

	return ctrl.NewControllerManagedBy(mgr).
		For(u, builder.WithPredicates(pred)).
		Complete(r)
}

func (r *VciReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := crlog.FromContext(ctx).WithValues("vci", req.NamespacedName)

	// Fetch VCI (unstructured)
	var vci unstructured.Unstructured
	vci.SetGroupVersionKind(gvkVCI)
	if err := r.Get(ctx, req.NamespacedName, &vci); err != nil {
		if apierrors.IsNotFound(err) {
			// VCI deleted: GC secrets + AccessKey + token secret
			_ = r.gcAllFluxSecretsForVCI(ctx, req.Namespace, req.Name)
			_ = r.deleteAccessKey(ctx, req.Name)
			_ = r.deleteTokenSecret(ctx, req.Name)
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	phase, _, _ := unstructured.NestedString(vci.Object, "status", "phase")
	if phase != "Ready" {
		log.V(1).Info("VCI not Ready yet", "phase", phase)
		return ctrl.Result{}, nil
	}

	// 1) Ensure AccessKey + token Secret
	token, err := r.ensureAccessKeyAndToken(ctx, &vci)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("ensure access key: %w", err)
	}

	// 2) Build kubeconfig bytes
	project := projectFromNamespace(vci.GetNamespace())
	serverURL, err := renderServerURL(r.Opts.ServerTemplate, serverVars{
		Domain:    r.Opts.LoftDomain,
		Project:   project,
		Namespace: vci.GetNamespace(),
		Name:      vci.GetName(),
	})
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("render server url: %w", err)
	}

	var caPEM []byte
	if r.Opts.CASecretNS != "" && r.Opts.CASecretName != "" {
		var ca corev1.Secret
		if err := r.Get(ctx, types.NamespacedName{Name: r.Opts.CASecretName, Namespace: r.Opts.CASecretNS}, &ca); err == nil {
			caPEM = ca.Data[r.Opts.CASecretKey]
		}
	}

	kcfgBytes, ksum, err := buildKubeconfigBytes(serverURL, vci.GetName(), token, caPEM)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("build kubeconfig: %w", err)
	}

	// 3) Resolve Flux namespaces (exact + globs)
	nsList, err := r.resolveFluxNamespaces(ctx)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("resolve namespaces: %w", err)
	}
	for _, ns := range nsList {
		if err := r.upsertFluxSecretInNS(ctx, &vci, ns, kcfgBytes, ksum); err != nil {
			return ctrl.Result{}, fmt.Errorf("upsert secret in %s: %w", ns, err)
		}
	}

	log.V(1).Info("reconciled VCI", "namespaces", strings.Join(nsList, ","))
	return ctrl.Result{}, nil
}

// ----- helpers -----

func (r *VciReconciler) upsertFluxSecretInNS(ctx context.Context, vci *unstructured.Unstructured, ns string, kcfg []byte, sumHex string) error {
	name := r.secretNameFor(vci.GetName())
	k := r.Opts.SecretKey
	want := map[string][]byte{k: kcfg}

	lbl := map[string]string{
		"app.kubernetes.io/managed-by": "vcluster-platform-flux-secret-controller",
		"fluxcd.io/kubeconfig":         "true",
		"vci.flux.loft.sh/name":        vci.GetName(),
		"vci.flux.loft.sh/namespace":   vci.GetNamespace(),
	}
	ann := map[string]string{
		"vci.flux.loft.sh/kcfg-sha256": sumHex,
	}

	var existing corev1.Secret
	err := r.Get(ctx, types.NamespacedName{Namespace: ns, Name: name}, &existing)
	if apierrors.IsNotFound(err) {
		sec := corev1.Secret{
			ObjectMeta: meta.ObjectMeta{
				Name:        name,
				Namespace:   ns,
				Labels:      lbl,
				Annotations: ann,
			},
			Type: corev1.SecretTypeOpaque,
			Data: want,
		}
		return r.Create(ctx, &sec)
	} else if err != nil {
		return err
	}

	changed := base64.StdEncoding.EncodeToString(existing.Data[k]) != base64.StdEncoding.EncodeToString(want[k]) ||
		existing.Annotations["vci.flux.loft.sh/kcfg-sha256"] != sumHex
	if changed {
		existing.Data = want
		if existing.Annotations == nil {
			existing.Annotations = map[string]string{}
		}
		existing.Annotations["vci.flux.loft.sh/kcfg-sha256"] = sumHex
		if existing.Labels == nil {
			existing.Labels = map[string]string{}
		}
		for k, v := range lbl {
			existing.Labels[k] = v
		}
		return r.Update(ctx, &existing)
	}
	return nil
}

func (r *VciReconciler) gcAllFluxSecretsForVCI(ctx context.Context, vciNamespace, vciName string) error {
	var list corev1.SecretList
	sel := labels.SelectorFromSet(map[string]string{
		"app.kubernetes.io/managed-by": "vcluster-platform-flux-secret-controller",
		"vci.flux.loft.sh/name":        vciName,
		"vci.flux.loft.sh/namespace":   vciNamespace,
	})
	if err := r.List(ctx, &list, &client.ListOptions{LabelSelector: sel}); err != nil {
		return err
	}
	for i := range list.Items {
		_ = r.Delete(ctx, &list.Items[i])
	}
	return nil
}

func (r *VciReconciler) deleteAccessKey(ctx context.Context, vciName string) error {
	ak := unstructured.Unstructured{}
	ak.SetGroupVersionKind(gvkAK)
	ak.SetName(accessKeyName(vciName))
	return client.IgnoreNotFound(r.Delete(ctx, &ak))
}

func (r *VciReconciler) deleteTokenSecret(ctx context.Context, vciName string) error {
	return client.IgnoreNotFound(r.Delete(ctx, &corev1.Secret{
		ObjectMeta: meta.ObjectMeta{
			Name:      tokenSecretName(r.Opts.SecretPrefix, vciName),
			Namespace: r.Opts.ControllerNamespace,
		},
	}))
}

func (r *VciReconciler) ensureAccessKeyAndToken(ctx context.Context, vci *unstructured.Unstructured) (string, error) {
	// 0) Reuse token if present
	var tokSec corev1.Secret
	tokName := tokenSecretName(r.Opts.SecretPrefix, vci.GetName())
	if err := r.Get(ctx, types.NamespacedName{Name: tokName, Namespace: r.Opts.ControllerNamespace}, &tokSec); err == nil {
		if b, ok := tokSec.Data["token"]; ok && len(b) > 0 {
			return string(b), nil
		}
	}

	// 1) Mint token
	token, err := randomToken(48)
	if err != nil {
		return "", err
	}

	// 2) Upsert AccessKey (unstructured)
	ak := unstructured.Unstructured{}
	ak.SetGroupVersionKind(gvkAK)
	ak.SetName(accessKeyName(vci.GetName()))

	project := projectFromNamespace(vci.GetNamespace())
	spec := map[string]any{
		"key":  token,
		"type": "Other",
		"scope": map[string]any{
			"roles": []any{
				map[string]any{"role": "vcluster"},
			},
			"virtualClusters": []any{
				map[string]any{"project": project, "virtualCluster": vci.GetName()},
			},
		},
		"groups": []any{
			fmt.Sprintf("loft:vcluster:%s:%s", vci.GetNamespace(), vci.GetName()),
			"loft:system:vclusters",
		},
	}
	err = r.Get(ctx, types.NamespacedName{Name: ak.GetName()}, &ak)
	if apierrors.IsNotFound(err) {
		ak.SetLabels(map[string]string{
			"loft.sh/vcluster":                    "true",
			"loft.sh/vcluster-instance-name":      vci.GetName(),
			"loft.sh/vcluster-instance-namespace": vci.GetNamespace(),
		})
		_ = unstructured.SetNestedField(ak.Object, spec, "spec")
		if err := r.Create(ctx, &ak); err != nil {
			return "", err
		}
	} else if err == nil {
		_ = unstructured.SetNestedField(ak.Object, spec, "spec")
		lbl := ak.GetLabels()
		if lbl == nil {
			lbl = map[string]string{}
		}
		lbl["loft.sh/vcluster"] = "true"
		lbl["loft.sh/vcluster-instance-name"] = vci.GetName()
		lbl["loft.sh/vcluster-instance-namespace"] = vci.GetNamespace()
		ak.SetLabels(lbl)
		if err := r.Update(ctx, &ak); err != nil {
			return "", err
		}
	} else {
		return "", err
	}

	// 3) Persist token secret
	save := corev1.Secret{
		ObjectMeta: meta.ObjectMeta{
			Name:      tokName,
			Namespace: r.Opts.ControllerNamespace,
			Labels: map[string]string{
				"app.kubernetes.io/managed-by": "vcluster-platform-flux-secret-controller",
			},
			Annotations: map[string]string{
				"vci.flux.loft.sh/vci": fmt.Sprintf("%s/%s", vci.GetNamespace(), vci.GetName()),
			},
		},
		Type: corev1.SecretTypeOpaque,
		Data: map[string][]byte{"token": []byte(token)},
	}
	if err := r.Create(ctx, &save); err != nil {
		if apierrors.IsAlreadyExists(err) {
			if e2 := r.Get(ctx, types.NamespacedName{Name: tokName, Namespace: r.Opts.ControllerNamespace}, &tokSec); e2 == nil {
				tokSec.Data["token"] = []byte(token)
				if tokSec.Annotations == nil {
					tokSec.Annotations = map[string]string{}
				}
				tokSec.Annotations["vci.flux.loft.sh/vci"] = fmt.Sprintf("%s/%s", vci.GetNamespace(), vci.GetName())
				if e3 := r.Update(ctx, &tokSec); e3 != nil {
					return "", e3
				}
			}
		} else {
			return "", err
		}
	}

	return token, nil
}

func randomToken(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

func (r *VciReconciler) secretNameFor(vciName string) string {
	return fmt.Sprintf("%s%s-kubeconfig", r.Opts.SecretPrefix, vciName)
}

func tokenSecretName(prefix, vciName string) string {
	return fmt.Sprintf("%s%s-ak", prefix, vciName)
}

func accessKeyName(vciName string) string {
	return fmt.Sprintf("loft-vcluster-%s", vciName)
}

// projectFromNamespace: best-effort derivation; customize if you store an explicit label.
func projectFromNamespace(ns string) string {
	// common Loft convention: "p-<project>"
	if len(ns) > 2 && ns[:2] == "p-" {
		return ns[2:]
	}
	return "default"
}
