package api

import (
	"net/http"

	"github.com/go-chi/chi/v5"
)

// --- Proposals (proxied to remote glossary-server) ---

// TeamCreateProposal submits a new add/edit/delete proposal.
func (h *Handler) TeamCreateProposal(w http.ResponseWriter, r *http.Request) {
	h.teamProxy(w, http.MethodPost, "/api/proposals", decodeBody(r))
}

// TeamMyProposals lists the caller's own proposals.
func (h *Handler) TeamMyProposals(w http.ResponseWriter, r *http.Request) {
	h.teamProxy(w, http.MethodGet, "/api/proposals/mine", nil)
}

// TeamWithdrawProposal withdraws the caller's pending proposal.
func (h *Handler) TeamWithdrawProposal(w http.ResponseWriter, r *http.Request) {
	h.teamProxy(w, http.MethodDelete, "/api/proposals/"+cleanID(chi.URLParam(r, "id")), nil)
}

// TeamPendingProposals lists pending proposals the caller may review.
func (h *Handler) TeamPendingProposals(w http.ResponseWriter, r *http.Request) {
	path := "/api/proposals"
	if c := r.URL.Query().Get("category"); c != "" {
		path += "?category=" + c
	}
	h.teamProxy(w, http.MethodGet, path, nil)
}

// TeamApproveProposal approves a proposal (1-of-N).
func (h *Handler) TeamApproveProposal(w http.ResponseWriter, r *http.Request) {
	h.teamProxy(w, http.MethodPost, "/api/proposals/"+cleanID(chi.URLParam(r, "id"))+"/approve", decodeBody(r))
}

// TeamRejectProposal rejects a proposal with a note.
func (h *Handler) TeamRejectProposal(w http.ResponseWriter, r *http.Request) {
	h.teamProxy(w, http.MethodPost, "/api/proposals/"+cleanID(chi.URLParam(r, "id"))+"/reject", decodeBody(r))
}

// --- Admin (proxied; remote enforces superadmin) ---

func (h *Handler) TeamCreateUser(w http.ResponseWriter, r *http.Request) {
	h.teamProxy(w, http.MethodPost, "/api/admin/users", decodeBody(r))
}

func (h *Handler) TeamSetReviewer(w http.ResponseWriter, r *http.Request) {
	h.teamProxy(w, http.MethodPost, "/api/admin/reviewers", decodeBody(r))
}

func (h *Handler) TeamListUsers(w http.ResponseWriter, r *http.Request) {
	h.teamProxy(w, http.MethodGet, "/api/admin/users", nil)
}

// --- Account self-service (any logged-in member) ---

// TeamChangePassword changes the caller's own password.
func (h *Handler) TeamChangePassword(w http.ResponseWriter, r *http.Request) {
	h.teamProxy(w, http.MethodPost, "/api/auth/password", decodeBody(r))
}

// TeamUpdateProfile changes the caller's own display name.
func (h *Handler) TeamUpdateProfile(w http.ResponseWriter, r *http.Request) {
	h.teamProxy(w, http.MethodPost, "/api/me", decodeBody(r))
}

// TeamUserList returns the read-only user list (any member).
func (h *Handler) TeamUserList(w http.ResponseWriter, r *http.Request) {
	h.teamProxy(w, http.MethodGet, "/api/users", nil)
}

// --- Admin user management (remote enforces superadmin) ---

func (h *Handler) TeamSetUserRole(w http.ResponseWriter, r *http.Request) {
	h.teamProxy(w, http.MethodPost, "/api/admin/users/"+cleanID(chi.URLParam(r, "id"))+"/role", decodeBody(r))
}

func (h *Handler) TeamSetUserStatus(w http.ResponseWriter, r *http.Request) {
	h.teamProxy(w, http.MethodPost, "/api/admin/users/"+cleanID(chi.URLParam(r, "id"))+"/status", decodeBody(r))
}

func (h *Handler) TeamResetUserPassword(w http.ResponseWriter, r *http.Request) {
	h.teamProxy(w, http.MethodPost, "/api/admin/users/"+cleanID(chi.URLParam(r, "id"))+"/reset-password", decodeBody(r))
}

func (h *Handler) TeamDeleteUser(w http.ResponseWriter, r *http.Request) {
	h.teamProxy(w, http.MethodDelete, "/api/admin/users/"+cleanID(chi.URLParam(r, "id")), nil)
}

// --- Admin glossary bulk import (remote enforces superadmin) ---

// TeamBulkImport uploads the caller's full local glossary to the remote server
// in one request. The frontend sends { entries: [...] }; we proxy it through.
func (h *Handler) TeamBulkImport(w http.ResponseWriter, r *http.Request) {
	h.teamProxy(w, http.MethodPost, "/api/admin/glossary/bulk-import", decodeBody(r))
}
