// Copyright 2019 The Kubernetes Authors.
// SPDX-License-Identifier: Apache-2.0

// Helm chart generator
//
// Fetches the given chart from {ChartRepo}/{ChartName},
// and inflates it to stdout, using the given values file.
// This generator expects helm V3 or later.

//go:generate pluginator
package main

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path"
	"regexp"
	"strings"

	"github.com/pkg/errors"
	"sigs.k8s.io/kustomize/api/filesys"
	"sigs.k8s.io/kustomize/api/resmap"
	"sigs.k8s.io/kustomize/api/types"
	"sigs.k8s.io/yaml"
)

// HelmChartInflationGeneratorPlugin is a plugin to generate resources
// from a remote or local helm chart.
type HelmChartInflationGeneratorPlugin struct {
	h                *resmap.PluginHelpers
	types.ObjectMeta `json:"metadata,omitempty" yaml:"metadata,omitempty" protobuf:"bytes,1,opt,name=metadata"`
	runHelmCommand   func([]string) ([]byte, error)
	types.HelmChartArgs
	tmpDir string
}

//noinspection GoUnusedGlobalVariable
var KustomizePlugin HelmChartInflationGeneratorPlugin

// Config uses the input plugin configurations `config` to setup the generator
// options
func (p *HelmChartInflationGeneratorPlugin) Config(h *resmap.PluginHelpers, config []byte) error {
	p.h = h
	err := yaml.Unmarshal(config, p)
	if err != nil {
		return err
	}
	tmpDir, err := filesys.NewTmpConfirmedDir()
	if err != nil {
		return err
	}
	p.tmpDir = string(tmpDir)
	if p.ChartName == "" {
		return fmt.Errorf("chartName cannot be empty")
	}
	if p.ChartHome == "" {
		p.ChartHome = path.Join(p.tmpDir, "chart")
	}
	if p.ChartRepoName == "" {
		p.ChartRepoName = "stable"
	}
	if p.HelmBin == "" {
		p.HelmBin = "helm"
	}
	if p.HelmHome == "" {
		p.HelmHome = path.Join(p.tmpDir, ".helm")
	}
	if p.Values == "" {
		p.Values = path.Join(p.ChartHome, p.ChartName, "values.yaml")
	}
	// runHelmCommand will run `helm` command with args provided. Return stdout
	// and error if there is any.
	p.runHelmCommand = func(args []string) ([]byte, error) {
		stdout := new(bytes.Buffer)
		stderr := new(bytes.Buffer)
		cmd := exec.Command(p.HelmBin, args...)
		cmd.Stdout = stdout
		cmd.Stderr = stderr
		cmd.Env = append(cmd.Env,
			fmt.Sprintf("HELM_CONFIG_HOME=%s", p.HelmHome),
			fmt.Sprintf("HELM_CACHE_HOME=%s/.cache", p.HelmHome),
			fmt.Sprintf("HELM_DATA_HOME=%s/.data", p.HelmHome),
		)
		err := cmd.Run()
		if err != nil {
			return stdout.Bytes(),
				errors.Wrap(
					fmt.Errorf("failed to run command %s %s", p.HelmBin, strings.Join(args, " ")),
					stderr.String(),
				)
		}
		return stdout.Bytes(), nil
	}
	return nil
}

// Generate implements generator
func (p *HelmChartInflationGeneratorPlugin) Generate() (resmap.ResMap, error) {
	// cleanup
	defer os.RemoveAll(p.tmpDir)
	// check helm version. we only support V3
	err := p.checkHelmVersion()
	if err != nil {
		return nil, err
	}
	// pull the chart
	if !p.checkLocalChart() {
		_, err := p.runHelmCommand(p.getPullCommandArgs())
		if err != nil {
			return nil, err
		}
	}
	// render the charts
	stdout, err := p.runHelmCommand(p.getTemplateCommandArgs())
	if err != nil {
		return nil, err
	}

	return p.h.ResmapFactory().NewResMapFromBytes(stdout)
}

func (p *HelmChartInflationGeneratorPlugin) getTemplateCommandArgs() []string {
	args := []string{"template"}
	if p.ReleaseName != "" {
		args = append(args, p.ReleaseName)
	}
	args = append(args, path.Join(p.ChartHome, p.ChartName))
	if p.ReleaseNamespace != "" {
		args = append(args, "--namespace", p.ReleaseNamespace)
	}
	if p.Values != "" {
		args = append(args, "--values", p.Values)
	}
	args = append(args, p.ExtraArgs...)
	return args
}

func (p *HelmChartInflationGeneratorPlugin) getPullCommandArgs() []string {
	args := []string{"pull", "--untar", "--untardir", p.ChartHome}
	chartName := fmt.Sprintf("%s/%s", p.ChartRepoName, p.ChartName)
	if p.ChartVersion != "" {
		args = append(args, "--version", p.ChartVersion)
	}
	if p.ChartRepoURL != "" {
		args = append(args, "--repo", p.ChartRepoURL)
		chartName = p.ChartName
	}

	args = append(args, chartName)

	return args
}

// checkLocalChart will return true if the chart does exist in
// local chart home.
func (p *HelmChartInflationGeneratorPlugin) checkLocalChart() bool {
	path := path.Join(p.ChartHome, p.ChartName)
	s, err := os.Stat(path)
	if err != nil {
		return false
	}
	return s.IsDir()
}

// checkHelmVersion will return an error if the helm version is not V3
func (p *HelmChartInflationGeneratorPlugin) checkHelmVersion() error {
	stdout, err := p.runHelmCommand([]string{"version", "-c", "--short"})
	if err != nil {
		return err
	}
	r, err := regexp.Compile(`v\d+(\.\d+)+`)
	if err != nil {
		return err
	}
	v := string(r.Find(stdout))[1:]
	majorVersion := strings.Split(v, ".")[0]
	if majorVersion != "3" {
		return fmt.Errorf("this plugin requires helm V3 but got v%s", v)
	}
	return nil
}
