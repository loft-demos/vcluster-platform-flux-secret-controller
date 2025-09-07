package controller

import (
	"context"
	"path/filepath"
	"strings"

	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func (r *VciReconciler) resolveFluxNamespaces(ctx context.Context) ([]string, error) {
	pats := r.Opts.FluxNamespacePatterns
	if len(pats) == 0 {
		pats = []string{"flux-system"}
	}
	// Trim + de-dup & track if any glob
	seen := map[string]struct{}{}
	exact := []string{}
	hasGlob := false
	for _, p := range pats {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		if _, ok := seen[p]; ok {
			continue
		}
		seen[p] = struct{}{}
		if strings.ContainsAny(p, "*?[]") {
			hasGlob = true
		} else {
			exact = append(exact, p)
		}
	}
	if !hasGlob {
		return exact, nil
	}

	var nsList corev1.NamespaceList
	if err := r.List(ctx, &nsList, &client.ListOptions{}); err != nil {
		return nil, err
	}
	out := map[string]struct{}{}
	for _, ns := range nsList.Items {
		name := ns.Name
		for p := range seen {
			ok, _ := filepath.Match(p, name)
			if ok {
				out[name] = struct{}{}
				break
			}
		}
	}
	for _, e := range exact {
		out[e] = struct{}{}
	}
	names := make([]string, 0, len(out))
	for k := range out {
		names = append(names, k)
	}
	return names, nil
}
