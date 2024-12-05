// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package collector

import (
	"context"
	"fmt"
	"reflect"

	"github.com/go-logr/logr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/open-telemetry/opentelemetry-operator/apis/v1beta1"
	"github.com/open-telemetry/opentelemetry-operator/internal/manifests"
	"github.com/open-telemetry/opentelemetry-operator/internal/version"
	collectorupgrade "github.com/open-telemetry/opentelemetry-operator/pkg/collector/upgrade"
)

const (
	eventTypeNormal  = "Normal"
	eventTypeWarning = "Warning"

	reasonError         = "Error"
	reasonStatusFailure = "StatusFailure"
	reasonInfo          = "Info"
)

// HandleReconcileStatus handles updating the status of the CRDs managed by the operator.
func HandleReconcileStatus(ctx context.Context, log logr.Logger, params manifests.Params, otelcol v1beta1.OpenTelemetryCollector, err error) (ctrl.Result, error) {
	log.V(2).Info("updating collector status")
	if err != nil {
		params.Recorder.Event(&otelcol, eventTypeWarning, reasonError, err.Error())
		return ctrl.Result{}, err
	}
	changed := otelcol.DeepCopy()

	// handle upgrades in case the instance got switched from unmanaged to managed mode
	if otelcol.Spec.UpgradeStrategy == v1beta1.UpgradeStrategyAutomatic {
		up := &collectorupgrade.VersionUpgrade{
			Log:      params.Log,
			Version:  version.Get(),
			Client:   params.Client,
			Recorder: params.Recorder,
		}
		upgraded, upgradeErr := up.ManagedInstance(ctx, *changed)
		if upgradeErr != nil {
			return ctrl.Result{}, fmt.Errorf("failed to upgrade the OpenTelemetry CR: %w", upgradeErr)
		}
		changed = &upgraded

		if !reflect.DeepEqual(otelcol, *changed) {
			// the resource update overrides the status, so keep a copy
			st := changed.Status

			patch := client.MergeFrom(&otelcol)
			if patchErr := params.Client.Patch(ctx, changed, patch); patchErr != nil {
				return ctrl.Result{}, fmt.Errorf("failed to apply changes to the OpenTelemetry CR: %w", patchErr)
			}

			changed.Status = st
			log.Info("instance upgraded", "version", changed.Status.Version)
		}
	}

	statusErr := UpdateCollectorStatus(ctx, params.Client, changed)
	if statusErr != nil {
		params.Recorder.Event(changed, eventTypeWarning, reasonStatusFailure, statusErr.Error())
		return ctrl.Result{}, statusErr
	}
	statusPatch := client.MergeFrom(&otelcol)
	if err := params.Client.Status().Patch(ctx, changed, statusPatch); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to apply status changes to the OpenTelemetry CR: %w", err)
	}
	params.Recorder.Event(changed, eventTypeNormal, reasonInfo, "applied status changes")
	return ctrl.Result{}, nil
}
