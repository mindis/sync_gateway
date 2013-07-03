//  Copyright (c) 2013 Couchbase, Inc.
//  Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file
//  except in compliance with the License. You may obtain a copy of the License at
//    http://www.apache.org/licenses/LICENSE-2.0
//  Unless required by applicable law or agreed to in writing, software distributed under the
//  License is distributed on an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND,
//  either express or implied. See the License for the specific language governing permissions
//  and limitations under the License.

package rest

import (
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"

	"github.com/couchbaselabs/sync_gateway/base"
	"github.com/couchbaselabs/sync_gateway/db"
)

// Shared context of HTTP handlers. It's important that this remain immutable, because the
// handlers will access it from multiple goroutines.
type ServerContext struct {
	config    *ServerConfig
	databases map[string]*db.DatabaseContext
}

func NewServerContext(config *ServerConfig) *ServerContext {
	return &ServerContext{
		config:    config,
		databases: map[string]*db.DatabaseContext{},
	}
}

func (sc *ServerContext) Close() {
	for _, ctx := range sc.databases {
		ctx.Close()
	}
}

func checkDbName(dbName string) error {
	if match, _ := regexp.MatchString(`^[a-z][-a-z0-9_$()+/]*$`, dbName); !match {
		return &base.HTTPError{http.StatusBadRequest,
			fmt.Sprintf("Illegal database name: %s", dbName)}
	}
	return nil
}

// Adds a database to the ServerContext given its Bucket.
func (sc *ServerContext) AddDatabase(bucket base.Bucket, dbName string, syncFun *string, nag bool) (*db.DatabaseContext, error) {
	if dbName == "" {
		dbName = bucket.GetName()
	}

	if err := checkDbName(dbName); err != nil {
		return nil, err
	}

	if sc.databases[dbName] != nil {
		return nil, &base.HTTPError{http.StatusConflict,
			fmt.Sprintf("Duplicate database name %q", dbName)}
	}

	dbcontext, err := db.NewDatabaseContext(dbName, bucket)
	if err != nil {
		return nil, err
	}
	if syncFun != nil {
		if err := dbcontext.ApplySyncFun(*syncFun); err != nil {
			return nil, err
		}
	}

	if dbcontext.ChannelMapper == nil {
		if nag {
			base.Warn("Sync function undefined; using default")
		}
	}

	sc.databases[dbName] = dbcontext
	return dbcontext, nil
}

// Adds a database to the ServerContext given its configuration.
func (sc *ServerContext) AddDatabaseFromConfig(config *DbConfig) error {
	server := "http://localhost:8091"
	pool := "default"
	bucketName := config.name

	if config.Server != nil {
		server = *config.Server
	}
	if config.Pool != nil {
		pool = *config.Pool
	}
	if config.Bucket != nil {
		bucketName = *config.Bucket
	}
	dbName := config.name
	if dbName == "" {
		dbName = bucketName
	}
	if err := checkDbName(dbName); err != nil {
		return err
	}

	// Connect to the bucket and add the database:
	bucket, err := db.ConnectToBucket(server, pool, bucketName)
	if err != nil {
		return err
	}
	context, err := sc.AddDatabase(bucket, dbName, config.Sync, true)
	if err != nil {
		return err
	}

	// Create default users & roles:
	if err := sc.installPrincipals(context, config.Roles, "role"); err != nil {
		return nil
	}
	return sc.installPrincipals(context, config.Users, "user")
}

func (sc *ServerContext) RemoveDatabase(dbName string) bool {
	context := sc.databases[dbName]
	if context == nil {
		return false
	}
	context.Close()
	delete(sc.databases, dbName)
	return true
}

func (sc *ServerContext) installPrincipals(context *db.DatabaseContext, spec map[string]json.RawMessage, what string) error {
	for name, data := range spec {
		isUsers := (what == "user")
		if name == "GUEST" && isUsers {
			name = ""
		}
		authenticator := context.Authenticator()
		newPrincipal, err := authenticator.UnmarshalPrincipal(data, name, 1, isUsers)
		if err != nil {
			return fmt.Errorf("Invalid config for %s %q: %v", what, name, err)
		}
		oldPrincipal, err := authenticator.GetPrincipal(newPrincipal.Name(), isUsers)
		if oldPrincipal == nil || name == "" {
			if err == nil {
				err = authenticator.Save(newPrincipal)
			}
			if err != nil {
				return fmt.Errorf("Couldn't create %s %q: %v", what, name, err)
			} else if name == "" {
				base.Log("Reset guest user to config")
			} else {
				base.Log("Created %s %q", what, name)
			}
		}
	}
	return nil
}