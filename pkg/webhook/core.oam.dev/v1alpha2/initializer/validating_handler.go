/*
 Copyright 2021. The KubeVela Authors.

 Licensed under the Apache License, Version 2.0 (the "License");
 you may not use this file except in compliance with the License.
 You may obtain a copy of the License at

     http://www.apache.org/licenses/LICENSE-2.0

 Unless required by applicable law or agreed to in writing, software
 distributed under the License is distributed on an "AS IS" BASIS,
 WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 See the License for the specific language governing permissions and
 limitations under the License.
*/

package initializer

import (
	"context"
	"fmt"
	"net/http"

	admissionv1 "k8s.io/api/admission/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/runtime/inject"
	"sigs.k8s.io/controller-runtime/pkg/webhook"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	"github.com/oam-dev/kubevela/apis/core.oam.dev/v1beta1"
	velatypes "github.com/oam-dev/kubevela/apis/types"
	controller "github.com/oam-dev/kubevela/pkg/controller/core.oam.dev"
	"github.com/oam-dev/kubevela/pkg/controller/utils"
)

// ValidatingHandler handles validation of initializer
type ValidatingHandler struct {
	Client client.Client

	// Decoder decodes object
	Decoder *admission.Decoder
}

var _ inject.Client = &ValidatingHandler{}

// InjectClient injects the client into the InitializerValidateHandler
func (h *ValidatingHandler) InjectClient(c client.Client) error {
	if h.Client != nil {
		return nil
	}
	h.Client = c
	return nil
}

var _ admission.DecoderInjector = &ValidatingHandler{}

// InjectDecoder injects the decoder into the ValidatingHandler
func (h *ValidatingHandler) InjectDecoder(d *admission.Decoder) error {
	h.Decoder = d
	return nil
}

var _ admission.Handler = &ValidatingHandler{}

// Handle validate initializer
func (h *ValidatingHandler) Handle(ctx context.Context, req admission.Request) admission.Response {
	init := &v1beta1.Initializer{}

	if req.Operation == admissionv1.Create || req.Operation == admissionv1.Update {
		err := h.Decoder.Decode(req, init)
		if err != nil {
			return admission.Errored(http.StatusBadRequest, err)
		}
		for _, depend := range init.Spec.DependsOn {
			_, err = utils.GetInitializer(ctx, h.Client, depend.Ref.Namespace, depend.Ref.Name)
			if err != nil {
				if apierrors.IsNotFound(err) && (depend.Ref.Namespace == "" || depend.Ref.Namespace == velatypes.DefaultKubeVelaNS) {
					_, err = utils.GetBuildInInitializer(ctx, h.Client, depend.Ref.Name)
					if err != nil {
						return admission.Denied(fmt.Sprintf("fail to get built-in dependOn Initializer %s from err: %s", depend.Ref.Name, err.Error()))
					}
					continue
				}
				return admission.Denied(fmt.Sprintf("DependOn Initializer %s not Found, err: %s", depend.Ref.Name, err.Error()))
			}
		}
	}

	if req.Operation == admissionv1.Delete {
		var obj client.ObjectKey
		obj.Name = req.Name
		obj.Namespace = req.Namespace
		err := h.Client.Get(ctx, obj, init)
		if err != nil {
			return admission.Errored(http.StatusNotFound, err)
		}
		// check if there are other initializers depends on this
		isDepended, dependingInitName, err := h.CheckInitDependedOn(ctx, init)
		if err != nil {
			return admission.Errored(http.StatusInternalServerError, err)
		}
		if isDepended {
			return admission.Denied(fmt.Sprintf("initializer %s still depends on this initializer", dependingInitName))
		}
	}
	return admission.ValidationResponse(true, "")
}

// CheckInitDependedOn check if there are other initializers depending on this init
func (h *ValidatingHandler) CheckInitDependedOn(ctx context.Context, obj *v1beta1.Initializer) (bool, string, error) {
	var (
		inits          v1beta1.InitializerList
		isDepended     bool
		dependInitName string
	)
	err := h.Client.List(ctx, &inits)
	if err != nil {
		return false, "", err
	}
	for _, i := range inits.Items {
		depends := i.Spec.DependsOn
		for _, d := range depends {
			if d.Ref.Name == obj.Name {
				dependInitName = i.Name
				isDepended = true
				break
			}
		}
		if isDepended {
			break
		}
	}
	return isDepended, dependInitName, nil
}

// RegisterValidatingHandler will register initializer validation to webhook
func RegisterValidatingHandler(mgr manager.Manager, args controller.Args) {
	server := mgr.GetWebhookServer()
	server.Register("/validating-core-oam-dev-v1beta1-initializers", &webhook.Admission{Handler: &ValidatingHandler{}})
}
