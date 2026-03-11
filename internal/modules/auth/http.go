package auth

import (
	"errors"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"litedns/internal/api"
)

const contextAdminKey = "auth_admin"

func RegisterRoutes(group *gin.RouterGroup, service *Service) {
	authGroup := group.Group("/auth")
	{
		authGroup.POST("/login", loginHandler(service))
		authGroup.Use(AuthMiddleware(service))
		authGroup.POST("/logout", logoutHandler(service))
		authGroup.POST("/change-password", changePasswordHandler(service))
		authGroup.GET("/me", meHandler(service))
	}
}

func AuthMiddleware(service *Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		authz := strings.TrimSpace(c.GetHeader("Authorization"))
		if !strings.HasPrefix(authz, "Bearer ") {
			api.Fail(c, http.StatusUnauthorized, api.CodeUnauthorized, "missing bearer token")
			c.Abort()
			return
		}
		token := strings.TrimSpace(strings.TrimPrefix(authz, "Bearer "))
		if token == "" {
			api.Fail(c, http.StatusUnauthorized, api.CodeUnauthorized, "missing bearer token")
			c.Abort()
			return
		}

		admin, err := service.Authenticate(c.Request.Context(), token)
		if err != nil {
			if errors.Is(err, ErrSessionInvalid) {
				api.Fail(c, http.StatusUnauthorized, api.CodeUnauthorized, "invalid or expired token")
			} else {
				api.Fail(c, http.StatusInternalServerError, api.CodeInternalErr, "authenticate failed")
			}
			c.Abort()
			return
		}

		c.Set(contextAdminKey, admin)
		c.Set("auth_token", token)
		c.Next()
	}
}

func CurrentAdmin(c *gin.Context) (AdminInfo, bool) {
	v, ok := c.Get(contextAdminKey)
	if !ok {
		return AdminInfo{}, false
	}
	admin, ok := v.(AdminInfo)
	return admin, ok
}

func loginHandler(service *Service) gin.HandlerFunc {
	type request struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	return func(c *gin.Context) {
		var req request
		if err := c.ShouldBindJSON(&req); err != nil {
			api.Fail(c, http.StatusBadRequest, api.CodeValidationErr, "invalid request body")
			return
		}
		if req.Username == "" || req.Password == "" {
			api.Fail(c, http.StatusBadRequest, api.CodeValidationErr, "username and password are required")
			return
		}

		res, err := service.Login(c.Request.Context(), req.Username, req.Password)
		if err != nil {
			if errors.Is(err, ErrInvalidCredential) {
				api.Fail(c, http.StatusUnauthorized, api.CodeUnauthorized, "invalid username or password")
				return
			}
			api.Fail(c, http.StatusInternalServerError, api.CodeInternalErr, "login failed")
			return
		}

		api.OK(c, res)
	}
}

func logoutHandler(service *Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		token, _ := c.Get("auth_token")
		_ = service.Logout(c.Request.Context(), token.(string))
		api.OK(c, gin.H{})
	}
}

func changePasswordHandler(service *Service) gin.HandlerFunc {
	type request struct {
		OldPassword string `json:"old_password"`
		NewPassword string `json:"new_password"`
	}
	return func(c *gin.Context) {
		admin, ok := CurrentAdmin(c)
		if !ok {
			api.Fail(c, http.StatusUnauthorized, api.CodeUnauthorized, "unauthorized")
			return
		}

		var req request
		if err := c.ShouldBindJSON(&req); err != nil {
			api.Fail(c, http.StatusBadRequest, api.CodeValidationErr, "invalid request body")
			return
		}
		if req.OldPassword == "" || req.NewPassword == "" {
			api.Fail(c, http.StatusBadRequest, api.CodeValidationErr, "old_password and new_password are required")
			return
		}

		err := service.ChangePassword(c.Request.Context(), admin.ID, req.OldPassword, req.NewPassword)
		if err != nil {
			switch {
			case errors.Is(err, ErrWeakPassword):
				api.Fail(c, http.StatusBadRequest, api.CodeValidationErr, err.Error())
			case errors.Is(err, ErrInvalidCredential):
				api.Fail(c, http.StatusBadRequest, api.CodeValidationErr, "old password is incorrect")
			default:
				api.Fail(c, http.StatusInternalServerError, api.CodeInternalErr, "change password failed")
			}
			return
		}

		api.OK(c, gin.H{})
	}
}

func meHandler(service *Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		admin, ok := CurrentAdmin(c)
		if !ok {
			api.Fail(c, http.StatusUnauthorized, api.CodeUnauthorized, "unauthorized")
			return
		}

		me, err := service.Me(c.Request.Context(), admin.ID)
		if err != nil {
			api.Fail(c, http.StatusInternalServerError, api.CodeInternalErr, "query current user failed")
			return
		}
		api.OK(c, me)
	}
}
