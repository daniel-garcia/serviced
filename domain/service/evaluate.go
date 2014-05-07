// Copyright 2014, The Serviced Authors. All rights reserved.
// Use of this source code is governed by a
// license that can be found in the LICENSE file.

package service

import (
	"github.com/zenoss/glog"

	"bytes"
	"encoding/json"
	"text/template"
)

type GetService interface {
	GetService(serviceID string, value *Service) error
}

func parent(gs GetService) func(s Service) (value Service, err error) {
	return func(s Service) (value Service, err error) {
		err = gs.GetService(s.ParentServiceId, &value)
		return
	}
}

func context(gs GetService) func(s Service) (ctx map[string]interface{}, err error) {
	return func(s Service) (ctx map[string]interface{}, err error) {
		err = json.Unmarshal([]byte(s.Context), &ctx)
		if err != nil {
			glog.Errorf("Error unmarshal service context Id=%s: %s -> %s", s.Id, s.Context, err)
		}
		return
	}
}

// EvaluateActionsTemplate parses and evaluates the Actions string of a service.
func (service *Service) EvaluateActionsTemplate(gs GetService) (err error) {
	for key, value := range service.Actions {
		result := service.evaluateTemplate(gs, value)
		if result != "" {
			service.Actions[key] = result
		}
	}
	return
}

// EvaluateStartupTemplate parses and evaluates the StartUp string of a service.
func (service *Service) EvaluateStartupTemplate(gs GetService) (err error) {

	result := service.evaluateTemplate(gs, service.Startup)
	if result != "" {
		service.Startup = result
	}

	return
}

// EvaluateRunsTemplate parses and evaluates the Runs string of a service.
func (service *Service) EvaluateRunsTemplate(gs GetService) (err error) {
	for key, value := range service.Runs {
		result := service.evaluateTemplate(gs, value)
		if result != "" {
			service.Runs[key] = result
		}
	}
	return
}

// evaluateTemplate takes a control plane client and template string and evaluates
// the template using the service as the context. If the template is invalid or there is an error
// then an empty string is returned.
func (service *Service) evaluateTemplate(gs GetService, serviceTemplate string) string {
	functions := template.FuncMap{
		"parent":  parent(gs),
		"context": context(gs),
	}
	// parse the template
	t := template.Must(template.New("ServiceDefinitionTemplate").Funcs(functions).Parse(serviceTemplate))

	// evaluate it
	var buffer bytes.Buffer
	err := t.Execute(&buffer, service)
	if err == nil {
		return buffer.String()
	}

	// something went wrong, warn them
	glog.Warning("Evaluating template %s produced the following error %s ", serviceTemplate, err)
	return ""
}

// EvaluateLogConfigTemplate parses and evals the Path, Type and all the values for the tags of the log
// configs. This happens for each LogConfig on the service.
func (service *Service) EvaluateLogConfigTemplate(gs GetService) (err error) {
	// evaluate the template for the LogConfig as well as the tags

	for i, logConfig := range service.LogConfigs {
		// Path
		result := service.evaluateTemplate(gs, logConfig.Path)
		if result != "" {
			service.LogConfigs[i].Path = result
		}
		// Type
		result = service.evaluateTemplate(gs, logConfig.Type)
		if result != "" {
			service.LogConfigs[i].Type = result
		}

		// Tags
		for j, tag := range logConfig.LogTags {
			result = service.evaluateTemplate(gs, tag.Value)
			if result != "" {
				service.LogConfigs[i].LogTags[j].Value = result
			}
		}
	}
	return
}

// EvaluateEndpointTemplates parses and evaluates the "ApplicationTemplate" property
// of each of the service endpoints for this service.
func (service *Service) EvaluateEndpointTemplates(gs GetService) (err error) {
	functions := template.FuncMap{
		"parent":  parent(gs),
		"context": context(gs),
	}

	for i, ep := range service.Endpoints {
		if ep.Application != "" && ep.ApplicationTemplate == "" {
			ep.ApplicationTemplate = ep.Application
			service.Endpoints[i].ApplicationTemplate = ep.Application
		}
		if ep.ApplicationTemplate != "" {
			t := template.Must(template.New(service.Name).Funcs(functions).Parse(ep.ApplicationTemplate))
			var buffer bytes.Buffer
			if err = t.Execute(&buffer, service); err == nil {
				service.Endpoints[i].Application = buffer.String()
			} else {
				return
			}
		}
	}
	return
}