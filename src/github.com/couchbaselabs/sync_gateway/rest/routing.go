//  Copyright (c) 2012 Couchbase, Inc.
//  Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file
//  except in compliance with the License. You may obtain a copy of the License at
//    http://www.apache.org/licenses/LICENSE-2.0
//  Unless required by applicable law or agreed to in writing, software distributed under the
//  License is distributed on an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND,
//  either express or implied. See the License for the specific language governing permissions
//  and limitations under the License.

package rest

import (
	"net/http"
	"strings"

	"github.com/gorilla/mux"
)

// Regexes that match database or doc ID component of a path.
// These are needed to avoid conflict with handlers that match special underscore-prefixed paths
// like "/_profile" and "/db/_all_docs".
const dbRegex = "[^_/][^/]*"
const docRegex = "[^_/][^/]*"

// Creates a GorillaMux router containing the basic HTTP handlers for a server.
// This is the common functionality of the public and admin ports.
// The 'admin' flag specifies whether or not authentication is needed.
func createHandler(sc *ServerContext, admin bool) (*mux.Router, *mux.Router) {
	r := mux.NewRouter()
	r.StrictSlash(true)
	// Global operations:
	r.Handle("/", makeHandler(sc, admin, (*handler).handleRoot)).Methods("GET", "HEAD")
	r.Handle("/_all_dbs", makeHandler(sc, admin, (*handler).handleAllDbs)).Methods("GET", "HEAD")

	// Operations on databases:
	r.Handle("/{db:"+dbRegex+"}/", makeHandler(sc, admin, (*handler).handleGetDB)).Methods("GET", "HEAD")
	r.Handle("/{db:"+dbRegex+"}/", makeHandler(sc, admin, (*handler).handlePostDoc)).Methods("POST")

	// Special database URLs:
	dbr := r.PathPrefix("/{db:" + dbRegex + "}/").Subrouter()
	dbr.StrictSlash(true)
	dbr.Handle("/_all_docs", makeHandler(sc, admin, (*handler).handleAllDocs)).Methods("GET", "HEAD", "POST")
	dbr.Handle("/_bulk_docs", makeHandler(sc, admin, (*handler).handleBulkDocs)).Methods("POST")
	dbr.Handle("/_bulk_get", makeHandler(sc, admin, (*handler).handleBulkGet)).Methods("GET", "HEAD")
	dbr.Handle("/_changes", makeHandler(sc, admin, (*handler).handleChanges)).Methods("GET", "HEAD")
	dbr.Handle("/_design/sync_gateway", makeHandler(sc, admin, (*handler).handleDesign)).Methods("GET", "HEAD")
	dbr.Handle("/_ensure_full_commit", makeHandler(sc, admin, (*handler).handleEFC)).Methods("POST")
	dbr.Handle("/_revs_diff", makeHandler(sc, admin, (*handler).handleRevsDiff)).Methods("POST")

	// Document URLs:
	dbr.Handle("/_local/{docid}", makeHandler(sc, admin, (*handler).handleGetLocalDoc)).Methods("GET", "HEAD")
	dbr.Handle("/_local/{docid}", makeHandler(sc, admin, (*handler).handlePutLocalDoc)).Methods("PUT")
	dbr.Handle("/_local/{docid}", makeHandler(sc, admin, (*handler).handleDelLocalDoc)).Methods("DELETE")

	dbr.Handle("/{docid:"+docRegex+"}", makeHandler(sc, admin, (*handler).handleGetDoc)).Methods("GET", "HEAD")
	dbr.Handle("/{docid:"+docRegex+"}", makeHandler(sc, admin, (*handler).handlePutDoc)).Methods("PUT")
	dbr.Handle("/{docid:"+docRegex+"}", makeHandler(sc, admin, (*handler).handleDeleteDoc)).Methods("DELETE")

	dbr.Handle("/{docid:"+docRegex+"}/{attach}", makeHandler(sc, admin, (*handler).handleGetAttachment)).Methods("GET", "HEAD")

	return r, dbr
}

// Creates the HTTP handler for the public API of a gateway server.
func CreatePublicHandler(sc *ServerContext) http.Handler {
	r, dbr := createHandler(sc, false)

	// Session/login URLs are per-database (unlike in CouchDB)
	// These always set 'admin' to true so that they can be called without being logged in already
	dbr.Handle("/_session", makeHandler(sc, true, (*handler).handleSessionGET)).Methods("GET", "HEAD")
	dbr.Handle("/_session", makeHandler(sc, true, (*handler).handleSessionPOST)).Methods("POST")
	if sc.config.Persona != nil {
		dbr.Handle("/_persona", makeHandler(sc, true, (*handler).handlePersonaPOST)).Methods("POST")
	}

	return wrapRouter(r)
}

//////// ADMIN API:

// Creates the HTTP handler for the PRIVATE admin API of a gateway server.
func CreateAdminHandler(sc *ServerContext) http.Handler {
	r, dbr := createHandler(sc, true)

	dbr.Handle("/_session",
		makeHandler(sc, true, (*handler).createUserSession)).Methods("POST")

	dbr.Handle("/_user/",
		makeHandler(sc, true, (*handler).getUsers)).Methods("GET", "HEAD")
	dbr.Handle("/_user/",
		makeHandler(sc, true, (*handler).putUser)).Methods("POST")
	dbr.Handle("/_user/{name}",
		makeHandler(sc, true, (*handler).getUserInfo)).Methods("GET", "HEAD")
	dbr.Handle("/_user/{name}",
		makeHandler(sc, true, (*handler).putUser)).Methods("PUT")
	dbr.Handle("/_user/{name}",
		makeHandler(sc, true, (*handler).deleteUser)).Methods("DELETE")

	dbr.Handle("/_role/",
		makeHandler(sc, true, (*handler).getRoles)).Methods("GET", "HEAD")
	dbr.Handle("/_role/",
		makeHandler(sc, true, (*handler).putRole)).Methods("POST")
	dbr.Handle("/_role/{name}",
		makeHandler(sc, true, (*handler).getRoleInfo)).Methods("GET", "HEAD")
	dbr.Handle("/_role/{name}",
		makeHandler(sc, true, (*handler).putRole)).Methods("PUT")
	dbr.Handle("/_role/{name}",
		makeHandler(sc, true, (*handler).deleteRole)).Methods("DELETE")

	r.Handle("/_profile",
		makeHandler(sc, true, (*handler).handleProfiling)).Methods("POST")

	// The routes below are part of the CouchDB REST API but should only be available to admins,
	// so the handlers are moved to the admin port.
	r.Handle("/{newdb:"+dbRegex+"}/",
		makeHandler(sc, true, (*handler).handleCreateDB)).Methods("PUT")
	r.Handle("/{db:"+dbRegex+"}/",
		makeHandler(sc, true, (*handler).handleDeleteDB)).Methods("DELETE")

	dbr.Handle("/_compact",
		makeHandler(sc, true, (*handler).handleCompact)).Methods("POST")
	dbr.Handle("/_vacuum",
		makeHandler(sc, true, (*handler).handleVacuum)).Methods("POST")
	dbr.Handle("/_dump/{view}",
		makeHandler(sc, true, (*handler).handleDump)).Methods("GET")

	return wrapRouter(r)
}

// Returns a top-level HTTP handler for a Router. This adds behavior for URLs that don't
// match anything -- it handles the OPTIONS method as well as returning either a 404 or 405
// for URLs that don't match a route.
func wrapRouter(router *mux.Router) http.Handler {
	return http.HandlerFunc(func(response http.ResponseWriter, rq *http.Request) {
		var match mux.RouteMatch
		if router.Match(rq, &match) {
			router.ServeHTTP(response, rq)
		} else {
			// What methods would have matched?
			var options []string
			for _, method := range []string{"GET", "HEAD", "POST", "PUT", "DELETE"} {
				if wouldMatch(router, rq, method) {
					options = append(options, method)
				}
			}
			if len(options) == 0 {
				response.WriteHeader(http.StatusNotFound)
			} else {
				response.Header().Add("Allow", strings.Join(options, ", "))
				if rq.Method != "OPTIONS" {
					response.WriteHeader(http.StatusMethodNotAllowed)
				}
			}
		}
	})
}

func wouldMatch(router *mux.Router, rq *http.Request, method string) bool {
	savedMethod := rq.Method
	rq.Method = method
	defer func() { rq.Method = savedMethod }()
	var matchInfo mux.RouteMatch
	return router.Match(rq, &matchInfo)
}
