package controller

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"text/template"
)

type serverVars struct {
	Domain    string
	Project   string
	Namespace string
	Name      string
}

type Kubeconfig struct {
	APIVersion     string `json:"apiVersion"`
	Kind           string `json:"kind"`
	Clusters       []struct {
		Name    string `json:"name"`
		Cluster struct {
			Server                   string `json:"server"`
			CertificateAuthorityData string `json:"certificate-authority-data,omitempty"`
			InsecureSkipTLSVerify    bool   `json:"insecure-skip-tls-verify,omitempty"`
		} `json:"cluster"`
	} `json:"clusters"`
	Contexts []struct {
		Name    string `json:"name"`
		Context struct {
			Cluster string `json:"cluster"`
			User    string `json:"user"`
		} `json:"context"`
	} `json:"contexts"`
	CurrentContext string `json:"current-context"`
	Users          []struct {
		Name string `json:"name"`
		User struct {
			Token string `json:"token"`
		} `json:"user"`
	} `json:"users"`
}

func renderServerURL(tmplStr string, vars serverVars) (string, error) {
	tmpl, err := template.New("server").Parse(tmplStr)
	if err != nil {
		return "", err
	}
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, vars); err != nil {
		return "", err
	}
	return buf.String(), nil
}

func buildKubeconfigBytes(server, name, token string, caPEM []byte) ([]byte, string, error) {
	cfg := Kubeconfig{
		APIVersion: "v1",
		Kind:       "Config",
	}
	cfg.Clusters = []struct {
		Name    string `json:"name"`
		Cluster struct {
			Server                   string `json:"server"`
			CertificateAuthorityData string `json:"certificate-authority-data,omitempty"`
			InsecureSkipTLSVerify    bool   `json:"insecure-skip-tls-verify,omitempty"`
		} `json:"cluster"`
	}{
		{
			Name: name,
			Cluster: func() (out struct {
				Server                   string `json:"server"`
				CertificateAuthorityData string `json:"certificate-authority-data,omitempty"`
				InsecureSkipTLSVerify    bool   `json:"insecure-skip-tls-verify,omitempty"`
			}) {
				out.Server = server
				if len(caPEM) > 0 {
					out.CertificateAuthorityData = base64.StdEncoding.EncodeToString(caPEM)
				}
				return out
			}(),
		},
	}
	cfg.Contexts = []struct {
		Name    string `json:"name"`
		Context struct {
			Cluster string `json:"cluster"`
			User    string `json:"user"`
		} `json:"context"`
	}{
		{
			Name: name,
			Context: struct {
				Cluster string `json:"cluster"`
				User    string `json:"user"`
			}{Cluster: name, User: name},
		},
	}
	cfg.CurrentContext = name
	cfg.Users = []struct {
		Name string `json:"name"`
		User struct {
			Token string `json:"token"`
		} `json:"user"`
	}{
		{
			Name: name,
			User: struct{ Token string `json:"token"` }{Token: token},
		},
	}

	// JSON kubeconfig is accepted by client-go/Flux
	j, err := json.Marshal(cfg)
	if err != nil {
		return nil, "", err
	}
	sum := sha256.Sum256(j)
	return j, fmt.Sprintf("%x", sum), nil
}
