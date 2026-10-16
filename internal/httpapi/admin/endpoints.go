package admin

import (
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/imfanee/supersbc/internal/cache"
	"github.com/imfanee/supersbc/internal/model"
	"github.com/imfanee/supersbc/internal/store"
)

func (h *Handler) mountEndpoints(r chi.Router) {
	r.Get("/carriers/{id}/endpoints", h.listEndpoints)
	r.With(operators).Put("/carriers/{id}/endpoints", h.setEndpoints)
	r.With(operators).Post("/carriers/{id}/endpoints/{endpointId}/revive", h.reviveEndpoint)
}

// endpointRow is an endpoint with its live state.
type endpointRow struct {
	model.CarrierEndpoint
	Gateway string `json:"gateway"`
	// State is UP, DOWN or UNKNOWN from the OPTIONS ping, "parked" when the
	// endpoint was taken out of rotation after unanswered calls (D-75).
	State        string `json:"state"`
	Parked       bool   `json:"parked"`
	ParkedForSec int    `json:"parked_for_seconds,omitempty"`
}

func (h *Handler) endpointRows(r *http.Request, c *model.Carrier, eps []model.CarrierEndpoint) []endpointRow {
	only := len(eps) == 1
	out := make([]endpointRow, 0, len(eps))
	for _, e := range eps {
		row := endpointRow{CarrierEndpoint: e, Gateway: e.GatewayName(c.Name, only), State: "UNKNOWN"}
		if h.Gateways != nil {
			row.State = h.Gateways.State(row.Gateway).Status
		}
		if h.Pipe != nil && h.Pipe.EndpointHealth() != nil {
			if d := h.Pipe.EndpointHealth().TTL(r.Context(), e.ID); d > 0 {
				row.Parked, row.ParkedForSec = true, int(d.Seconds())
			}
		}
		out = append(out, row)
	}
	return out
}

// listEndpoints godoc
// @Summary Signalling endpoints of a carrier with their live state (D-75)
// @Tags carriers
// @Produce json
// @Param id path string true "carrier id"
// @Success 200 {array} endpointRow
// @Router /carriers/{id}/endpoints [get]
func (h *Handler) listEndpoints(w http.ResponseWriter, r *http.Request) {
	id, ok := pathUUID(w, r, "id")
	if !ok {
		return
	}
	c, err := h.Store.CarrierByID(r.Context(), id)
	if err != nil {
		failErr(w, err)
		return
	}
	eps, err := h.Store.Endpoints(r.Context(), id)
	if err != nil {
		failErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, h.endpointRows(r, c, eps))
}

type endpointsInput struct {
	Endpoints []store.EndpointSpec `json:"endpoints" validate:"required,min=1,dive"`
}

// setEndpoints godoc
// @Summary Replace the endpoint list of a carrier (first one is the primary address)
// @Tags carriers
// @Accept json
// @Produce json
// @Param id path string true "carrier id"
// @Param body body endpointsInput true "endpoints"
// @Success 200 {array} endpointRow
// @Router /carriers/{id}/endpoints [put]
func (h *Handler) setEndpoints(w http.ResponseWriter, r *http.Request) {
	id, ok := pathUUID(w, r, "id")
	if !ok {
		return
	}
	var in endpointsInput
	if !h.decode(w, r, &in) {
		return
	}
	c, err := h.Store.CarrierByID(r.Context(), id)
	if err != nil {
		failErr(w, err)
		return
	}
	before, _ := h.Store.Endpoints(r.Context(), id)
	eps, err := h.Store.ReplaceEndpoints(r.Context(), id, in.Endpoints)
	if err != nil {
		failErr(w, err)
		return
	}
	h.audit(r, "carrier.endpoints", "carrier", id.String(), before, eps)
	h.publish(r.Context(), cache.ChanCarriersChanged, id.String())
	writeJSON(w, http.StatusOK, h.endpointRows(r, c, eps))
}

// reviveEndpoint godoc
// @Summary Put a parked endpoint back into rotation at once
// @Tags carriers
// @Produce json
// @Param id path string true "carrier id"
// @Param endpointId path string true "endpoint id"
// @Success 204
// @Router /carriers/{id}/endpoints/{endpointId}/revive [post]
func (h *Handler) reviveEndpoint(w http.ResponseWriter, r *http.Request) {
	epID, ok := pathUUID(w, r, "endpointId")
	if !ok {
		return
	}
	if h.Pipe != nil && h.Pipe.EndpointHealth() != nil {
		h.Pipe.EndpointHealth().Revive(r.Context(), epID)
	}
	h.audit(r, "carrier.endpoint_revive", "endpoint", epID.String(), nil, nil)
	w.WriteHeader(http.StatusNoContent)
}
