package user

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"sort"
	"sync"
	"time"

	xctx "github.com/clyso/ceph-api/pkg/ctx"
	"github.com/clyso/ceph-api/pkg/rados"
	"github.com/clyso/ceph-api/pkg/types"
	"github.com/rs/zerolog"
	"golang.org/x/crypto/bcrypt"
)

const (
	pwdBcryptCost = 3
	setDBMonCmd   = `{"prefix": "config-key set", "key": "mgr/dashboard/accessdb_v2"}`
	getDBMonCmd   = `{"prefix": "config-key get", "key": "mgr/dashboard/accessdb_v2"}`
)

func HasPermissions(ctx context.Context, scope Scope, perms ...Permission) error {
	permissions := xctx.GetPermissions(ctx)
	if len(permissions) == 0 {
		return types.ErrAccessDenied
	}
	for _, p := range perms {
		if !slices.Contains(permissions[string(scope)], p.String()) {
			return types.ErrAccessDenied
		}
	}
	return nil
}

func New(radosSvc *rados.Svc) (*Service, error) {
	res := &Service{radosSvc: radosSvc}
	if err := res.updateFromDB(context.Background()); err != nil {
		return nil, err
	}
	return res, nil
}

type Service struct {
	sync.RWMutex
	radosSvc *rados.Svc
	users    map[string]User
	roles    map[string]Role
}

func (s *Service) updateFromDB(ctx context.Context) error {
	s.users = map[string]User{}
	s.roles = map[string]Role{}
	cmdRes, err := s.radosSvc.ExecMon(ctx, getDBMonCmd)
	if err != nil {
		// TODO: Figure this out
		if errors.Is(err, types.RadosErrorNotFound) {
			return nil
		}
		return err
	}
	var res db
	if err = json.Unmarshal(cmdRes, &res); err != nil {
		return err
	}
	if len(res.Users) != 0 {
		s.users = res.Users
	}
	if len(res.Roles) != 0 {
		s.roles = res.Roles
	}
	return nil
}

func (s *Service) storeToDB(ctx context.Context) error {
	res, err := json.Marshal(&db{Users: s.users, Roles: s.roles, Version: 2})
	if err != nil {
		return err
	}
	_, err = s.radosSvc.ExecMonWithInputBuff(ctx, setDBMonCmd, res)
	return err
}

func (s *Service) ListUsers(ctx context.Context) ([]User, error) {
	s.RLock()
	defer s.RUnlock()
	res := make([]User, 0, len(s.users))
	for _, v := range s.users {
		res = append(res, v)
	}
	sort.Slice(res, func(i, j int) bool {
		return res[i].Username < res[j].Username
	})
	return res, nil
}

func (s *Service) GetUser(ctx context.Context, username string) (User, error) {
	s.RLock()
	defer s.RUnlock()
	res, ok := s.users[username]
	if !ok {
		return User{}, types.ErrNotFound
	}
	return res, nil
}

func (s *Service) GetPermissions(ctx context.Context, username string) map[string][]string {
	s.RLock()
	defer s.RUnlock()
	res := map[string][]string{}
	usr, ok := s.users[username]
	if !ok {
		return res
	}
	for _, role := range usr.Roles {
		for scope, perm := range s.roles[role].Permissions {
			res[scope] = append(res[scope], perm...)
		}
		for scope, perm := range systemRoleMap[role].Permissions {
			res[scope] = append(res[scope], perm...)
		}
	}
	for scope := range res {
		sort.Strings(res[scope])
		res[scope] = slices.Compact(res[scope])
	}
	return res
}

func (s *Service) UpdateUser(ctx context.Context, user User) error {
	s.Lock()
	defer s.Unlock()
	prev, ok := s.users[user.Username]
	if !ok {
		return types.ErrNotFound
	}
	if err := s.validateUseRoles(user); err != nil {
		return err
	}
	if user.Password != "" {
		pwdHash, err := bcrypt.GenerateFromPassword([]byte(user.Password), pwdBcryptCost)
		if err != nil {
			return err
		}
		user.Password = string(pwdHash)
	} else {
		user.Password = prev.Password
		user.PwdExpirationDate = prev.PwdExpirationDate
	}
	user.LastUpdate = int(time.Now().Unix())
	s.users[user.Username] = user
	err := s.storeToDB(ctx)
	if err != nil {
		//rollback changes
		if rollbackErr := s.updateFromDB(ctx); rollbackErr != nil {
			zerolog.Ctx(ctx).Err(rollbackErr).Msg("unable to rollback access db")
		}
		return err
	}
	return nil
}

func (s *Service) validateUseRoles(u User) error {
	for _, r := range u.Roles {
		_, exists := systemRoleMap[r]
		if !exists {
			_, exists = s.roles[r]
		}
		if !exists {
			return fmt.Errorf("%w: role %s not found", types.ErrInvalidArg, r)
		}
	}
	return nil
}

func (s *Service) CreateUser(ctx context.Context, user User) error {
	s.Lock()
	defer s.Unlock()
	if err := user.Validate(); err != nil {
		return err
	}
	if _, ok := s.users[user.Username]; ok {
		return types.ErrAlreadyExists
	}
	if err := s.validateUseRoles(user); err != nil {
		return err
	}
	user.LastUpdate = int(time.Now().Unix())

	pwdHash, err := bcrypt.GenerateFromPassword([]byte(user.Password), pwdBcryptCost)
	if err != nil {
		return err
	}
	user.Password = string(pwdHash)

	s.users[user.Username] = user
	err = s.storeToDB(ctx)
	if err != nil {
		//rollback changes
		if rollbackErr := s.updateFromDB(ctx); rollbackErr != nil {
			zerolog.Ctx(ctx).Err(rollbackErr).Msg("unable to rollback access db")
		}
		return err
	}
	return nil

}

func (s *Service) DeleteUser(ctx context.Context, username string) error {
	s.Lock()
	defer s.Unlock()
	delete(s.users, username)
	err := s.storeToDB(ctx)
	if err != nil {
		//rollback changes
		if rollbackErr := s.updateFromDB(ctx); rollbackErr != nil {
			zerolog.Ctx(ctx).Err(rollbackErr).Msg("unable to rollback access db")
		}
		return err
	}
	return nil
}

func (s *Service) ChangePassword(ctx context.Context, username, oldPass, newPass string) error {
	s.Lock()
	defer s.Unlock()
	user, ok := s.users[username]
	if !ok {
		return types.ErrNotFound
	}
	err := bcrypt.CompareHashAndPassword([]byte(user.Password), []byte(oldPass))
	if err != nil {
		return fmt.Errorf("%w: invalid old password", err)
	}
	passHash, err := bcrypt.GenerateFromPassword([]byte(newPass), pwdBcryptCost)
	if err != nil {
		return err
	}
	user.Password = string(passHash)
	s.users[username] = user
	err = s.storeToDB(ctx)
	if err != nil {
		//rollback changes
		if rollbackErr := s.updateFromDB(ctx); rollbackErr != nil {
			zerolog.Ctx(ctx).Err(rollbackErr).Msg("unable to rollback access db")
		}
		return err
	}
	return nil
}

type User struct {
	Username          string   `json:"username"`
	Roles             []string `json:"roles"`
	Password          string   `json:"password"`
	Name              *string  `json:"name"`
	Email             *string  `json:"email"`
	LastUpdate        int      `json:"lastUpdate"`
	Enabled           bool     `json:"enabled"`
	PwdExpirationDate *int     `json:"pwdExpirationDate"`
	PwdUpdateRequired bool     `json:"pwdUpdateRequired"`
}

func (u *User) Validate() error {
	if u.Username == "" {
		return fmt.Errorf("%w: username required", types.ErrInvalidArg)
	}

	if u.Password == "" {
		return fmt.Errorf("%w: password required", types.ErrInvalidArg)
	}
	return nil
}

type Role struct {
	Name        string              `json:"name"`
	Description *string             `json:"description"`
	IsSystem    bool                `json:"system"`
	Permissions map[string][]string `json:"scopes_permissions"`
}

func (r *Role) Validate() error {
	if r.Name == "" {
		return fmt.Errorf("%w: role name is empty", types.ErrInvalidArg)
	}
	for scope, perms := range r.Permissions {
		if _, ok := scopeSet[scope]; !ok {
			return fmt.Errorf("%w: unknown scope %s, valid values: %+q", types.ErrInvalidArg, scope, scopeSet)
		}

		for _, p := range perms {
			if _, ok := permissionSet[p]; !ok {
				return fmt.Errorf("%w: unknown permission %q, valid values %+q", types.ErrInvalidArg, p, permissionList)
			}
		}
	}
	return nil
}

type db struct {
	Users   map[string]User `json:"users"`
	Roles   map[string]Role `json:"roles"`
	Version int             `json:"version"`
}

func (s *Service) ListRoles(ctx context.Context) ([]Role, error) {
	s.RLock()
	defer s.RUnlock()
	res := make([]Role, 0, len(systemRoles)+len(s.roles))
	res = append(res, systemRoles...)
	for _, r := range s.roles {
		res = append(res, r)
	}
	return res, nil
}

func (s *Service) GetRole(ctx context.Context, name string) (Role, error) {
	s.RLock()
	defer s.RUnlock()
	role, ok := systemRoleMap[name]
	if !ok {
		role, ok = s.roles[name]
	}
	if !ok {
		return Role{}, types.ErrNotFound
	}
	return role, nil
}

func (s *Service) CreateRole(ctx context.Context, role Role) error {
	s.Lock()
	defer s.Unlock()
	_, exists := systemRoleMap[role.Name]
	if exists {
		return types.ErrAlreadyExists
	}
	_, exists = s.roles[role.Name]
	if exists {
		return types.ErrAlreadyExists
	}
	if err := role.Validate(); err != nil {
		return err
	}
	if role.IsSystem {
		return fmt.Errorf("%w: system role creation is not permitte", types.ErrInvalidArg)
	}
	s.roles[role.Name] = role

	if err := s.storeToDB(ctx); err != nil {
		//rollback changes
		if rollbackErr := s.updateFromDB(ctx); rollbackErr != nil {
			zerolog.Ctx(ctx).Err(rollbackErr).Msg("unable to rollback access db")
		}
		return err
	}
	return nil
}
func (s *Service) UpdateRole(ctx context.Context, role Role) error {
	s.Lock()
	defer s.Unlock()
	_, exists := systemRoleMap[role.Name]
	if exists || role.IsSystem {
		return fmt.Errorf("%w: cannot update system role", types.ErrInvalidArg)
	}
	_, exists = s.roles[role.Name]
	if !exists {
		return types.ErrNotFound
	}
	if err := role.Validate(); err != nil {
		return err
	}
	s.roles[role.Name] = role

	if err := s.storeToDB(ctx); err != nil {
		//rollback changes
		if rollbackErr := s.updateFromDB(ctx); rollbackErr != nil {
			zerolog.Ctx(ctx).Err(rollbackErr).Msg("unable to rollback access db")
		}
		return err
	}
	return nil
}

func (s *Service) DeleteRole(ctx context.Context, name string) error {
	s.Lock()
	defer s.Unlock()

	_, isSystem := systemRoleMap[name]
	if isSystem {
		return fmt.Errorf("%w: cannot delete system role", types.ErrInvalidArg)
	}
	_, exists := s.roles[name]
	if !exists {
		return types.ErrNotFound
	}
	for _, usr := range s.users {
		if slices.Contains(usr.Roles, name) {
			return fmt.Errorf("%w: role is in use", types.ErrInvalidArg)
		}
	}
	delete(s.roles, name)
	err := s.storeToDB(ctx)
	if err != nil {
		//rollback changes
		if rollbackErr := s.updateFromDB(ctx); rollbackErr != nil {
			zerolog.Ctx(ctx).Err(rollbackErr).Msg("unable to rollback access db")
		}
		return err
	}
	return nil
}

func (s *Service) CloneRole(ctx context.Context, srcName, dstName string) error {
	s.Lock()
	defer s.Unlock()
	src, ok := s.roles[srcName]
	if !ok {
		src, ok = systemRoleMap[srcName]
		if !ok {
			return types.ErrNotFound
		}
		src.IsSystem = false
	}
	src.Name = dstName
	s.roles[dstName] = src

	err := s.storeToDB(ctx)
	if err != nil {
		//rollback changes
		if rollbackErr := s.updateFromDB(ctx); rollbackErr != nil {
			zerolog.Ctx(ctx).Err(rollbackErr).Msg("unable to rollback access db")
		}
		return err
	}
	return nil
}
