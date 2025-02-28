// Copyright 2017 The Gitea Authors. All rights reserved.
// Use of this source code is governed by a MIT-style
// license that can be found in the LICENSE file.

package routes

import (
	"encoding/gob"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path"
	"strings"

	"code.gitea.io/gitea/models"
	"code.gitea.io/gitea/modules/context"
	auth "code.gitea.io/gitea/modules/forms"
	"code.gitea.io/gitea/modules/httpcache"
	"code.gitea.io/gitea/modules/lfs"
	"code.gitea.io/gitea/modules/log"
	"code.gitea.io/gitea/modules/metrics"
	"code.gitea.io/gitea/modules/public"
	"code.gitea.io/gitea/modules/setting"
	"code.gitea.io/gitea/modules/storage"
	"code.gitea.io/gitea/modules/templates"
	"code.gitea.io/gitea/modules/util"
	"code.gitea.io/gitea/modules/validation"
	"code.gitea.io/gitea/modules/web"
	"code.gitea.io/gitea/routers"
	"code.gitea.io/gitea/routers/admin"
	apiv1 "code.gitea.io/gitea/routers/api/v1"
	"code.gitea.io/gitea/routers/api/v1/misc"
	"code.gitea.io/gitea/routers/dev"
	"code.gitea.io/gitea/routers/events"
	"code.gitea.io/gitea/routers/org"
	"code.gitea.io/gitea/routers/private"
	"code.gitea.io/gitea/routers/repo"
	"code.gitea.io/gitea/routers/user"
	userSetting "code.gitea.io/gitea/routers/user/setting"
	"code.gitea.io/gitea/services/mailer"

	// to registers all internal adapters
	_ "code.gitea.io/gitea/modules/session"

	"gitea.com/go-chi/captcha"
	"gitea.com/go-chi/session"
	"github.com/NYTimes/gziphandler"
	"github.com/chi-middleware/proxy"
	"github.com/go-chi/chi/middleware"
	"github.com/go-chi/cors"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/tstranex/u2f"
	"github.com/unknwon/com"
)

const (
	// GzipMinSize represents min size to compress for the body size of response
	GzipMinSize = 1400
)

func commonMiddlewares() []func(http.Handler) http.Handler {
	var handlers = []func(http.Handler) http.Handler{
		func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(resp http.ResponseWriter, req *http.Request) {
				next.ServeHTTP(context.NewResponse(resp), req)
			})
		},
	}

	if setting.ReverseProxyLimit > 0 {
		opt := proxy.NewForwardedHeadersOptions().
			WithForwardLimit(setting.ReverseProxyLimit).
			ClearTrustedProxies()
		for _, n := range setting.ReverseProxyTrustedProxies {
			if !strings.Contains(n, "/") {
				opt.AddTrustedProxy(n)
			} else {
				opt.AddTrustedNetwork(n)
			}
		}
		handlers = append(handlers, proxy.ForwardedHeaders(opt))
	}

	handlers = append(handlers, middleware.StripSlashes)

	if !setting.DisableRouterLog && setting.RouterLogLevel != log.NONE {
		if log.GetLogger("router").GetLevel() <= setting.RouterLogLevel {
			handlers = append(handlers, LoggerHandler(setting.RouterLogLevel))
		}
	}

	handlers = append(handlers, func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(resp http.ResponseWriter, req *http.Request) {
			// Why we need this? The Recovery() will try to render a beautiful
			// error page for user, but the process can still panic again, and other
			// middleware like session also may panic then we have to recover twice
			// and send a simple error page that should not panic any more.
			defer func() {
				if err := recover(); err != nil {
					combinedErr := fmt.Sprintf("PANIC: %v\n%s", err, string(log.Stack(2)))
					log.Error("%v", combinedErr)
					if setting.IsProd() {
						http.Error(resp, http.StatusText(500), 500)
					} else {
						http.Error(resp, combinedErr, 500)
					}
				}
			}()
			next.ServeHTTP(resp, req)
		})
	})
	return handlers
}

// NormalRoutes represents non install routes
func NormalRoutes() *web.Route {
	r := web.NewRoute()
	for _, middle := range commonMiddlewares() {
		r.Use(middle)
	}

	r.Mount("/", WebRoutes())
	r.Mount("/api/v1", apiv1.Routes())
	r.Mount("/api/internal", private.Routes())
	return r
}

// WebRoutes returns all web routes
func WebRoutes() *web.Route {
	r := web.NewRoute()

	r.Use(session.Sessioner(session.Options{
		Provider:       setting.SessionConfig.Provider,
		ProviderConfig: setting.SessionConfig.ProviderConfig,
		CookieName:     setting.SessionConfig.CookieName,
		CookiePath:     setting.SessionConfig.CookiePath,
		Gclifetime:     setting.SessionConfig.Gclifetime,
		Maxlifetime:    setting.SessionConfig.Maxlifetime,
		Secure:         setting.SessionConfig.Secure,
		Domain:         setting.SessionConfig.Domain,
	}))

	r.Use(Recovery())

	r.Use(public.Custom(
		&public.Options{
			SkipLogging: setting.DisableRouterLog,
		},
	))
	r.Use(public.Static(
		&public.Options{
			Directory:   path.Join(setting.StaticRootPath, "public"),
			SkipLogging: setting.DisableRouterLog,
		},
	))

	r.Use(storageHandler(setting.Avatar.Storage, "avatars", storage.Avatars))
	r.Use(storageHandler(setting.RepoAvatar.Storage, "repo-avatars", storage.RepoAvatars))

	gob.Register(&u2f.Challenge{})

	if setting.EnableGzip {
		h, err := gziphandler.GzipHandlerWithOpts(gziphandler.MinSize(GzipMinSize))
		if err != nil {
			log.Fatal("GzipHandlerWithOpts failed: %v", err)
		}
		r.Use(h)
	}

	if (setting.Protocol == setting.FCGI || setting.Protocol == setting.FCGIUnix) && setting.AppSubURL != "" {
		r.Use(func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(resp http.ResponseWriter, req *http.Request) {
				req.URL.Path = strings.TrimPrefix(req.URL.Path, setting.AppSubURL)
				next.ServeHTTP(resp, req)
			})
		})
	}

	mailer.InitMailRender(templates.Mailer())

	if setting.Service.EnableCaptcha {
		r.Use(captcha.Captchaer(context.GetImageCaptcha()))
	}
	// Removed: toolbox.Toolboxer middleware will provide debug informations which seems unnecessary
	r.Use(context.Contexter())
	// GetHead allows a HEAD request redirect to GET if HEAD method is not defined for that route
	r.Use(middleware.GetHead)

	if setting.EnableAccessLog {
		r.Use(context.AccessLogger())
	}

	r.Use(user.GetNotificationCount)
	r.Use(repo.GetActiveStopwatch)
	r.Use(func(ctx *context.Context) {
		ctx.Data["UnitWikiGlobalDisabled"] = models.UnitTypeWiki.UnitGlobalDisabled()
		ctx.Data["UnitIssuesGlobalDisabled"] = models.UnitTypeIssues.UnitGlobalDisabled()
		ctx.Data["UnitPullsGlobalDisabled"] = models.UnitTypePullRequests.UnitGlobalDisabled()
		ctx.Data["UnitProjectsGlobalDisabled"] = models.UnitTypeProjects.UnitGlobalDisabled()
	})

	// for health check
	r.Head("/", func(w http.ResponseWriter, req *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	if setting.HasRobotsTxt {
		r.Get("/robots.txt", func(w http.ResponseWriter, req *http.Request) {
			filePath := path.Join(setting.CustomPath, "robots.txt")
			fi, err := os.Stat(filePath)
			if err == nil && httpcache.HandleTimeCache(req, w, fi) {
				return
			}
			http.ServeFile(w, req, filePath)
		})
	}

	r.Get("/apple-touch-icon.png", func(w http.ResponseWriter, req *http.Request) {
		http.Redirect(w, req, path.Join(setting.StaticURLPrefix, "img/apple-touch-icon.png"), 301)
	})

	// prometheus metrics endpoint
	if setting.Metrics.Enabled {
		c := metrics.NewCollector()
		prometheus.MustRegister(c)

		r.Get("/metrics", routers.Metrics)
	}

	if setting.API.EnableSwagger {
		// Note: The route moved from apiroutes because it's in fact want to render a web page
		r.Get("/api/swagger", misc.Swagger) // Render V1 by default
	}

	RegisterRoutes(r)

	return r
}

func goGet(ctx *context.Context) {
	if ctx.Query("go-get") != "1" {
		return
	}

	// Quick responses appropriate go-get meta with status 200
	// regardless of if user have access to the repository,
	// or the repository does not exist at all.
	// This is particular a workaround for "go get" command which does not respect
	// .netrc file.

	ownerName := ctx.Params(":username")
	repoName := ctx.Params(":reponame")
	trimmedRepoName := strings.TrimSuffix(repoName, ".git")

	if ownerName == "" || trimmedRepoName == "" {
		_, _ = ctx.Write([]byte(`<!doctype html>
<html>
	<body>
		invalid import path
	</body>
</html>
`))
		ctx.Status(400)
		return
	}
	branchName := setting.Repository.DefaultBranch

	repo, err := models.GetRepositoryByOwnerAndName(ownerName, repoName)
	if err == nil && len(repo.DefaultBranch) > 0 {
		branchName = repo.DefaultBranch
	}
	prefix := setting.AppURL + path.Join(url.PathEscape(ownerName), url.PathEscape(repoName), "src", "branch", util.PathEscapeSegments(branchName))

	appURL, _ := url.Parse(setting.AppURL)

	insecure := ""
	if appURL.Scheme == string(setting.HTTP) {
		insecure = "--insecure "
	}
	ctx.Header().Set("Content-Type", "text/html")
	ctx.Status(http.StatusOK)
	_, _ = ctx.Write([]byte(com.Expand(`<!doctype html>
<html>
	<head>
		<meta name="go-import" content="{GoGetImport} git {CloneLink}">
		<meta name="go-source" content="{GoGetImport} _ {GoDocDirectory} {GoDocFile}">
	</head>
	<body>
		go get {Insecure}{GoGetImport}
	</body>
</html>
`, map[string]string{
		"GoGetImport":    context.ComposeGoGetImport(ownerName, trimmedRepoName),
		"CloneLink":      models.ComposeHTTPSCloneURL(ownerName, repoName),
		"GoDocDirectory": prefix + "{/dir}",
		"GoDocFile":      prefix + "{/dir}/{file}#L{line}",
		"Insecure":       insecure,
	})))
}

// RegisterRoutes register routes
func RegisterRoutes(m *web.Route) {
	reqSignIn := context.Toggle(&context.ToggleOptions{SignInRequired: true})
	ignSignIn := context.Toggle(&context.ToggleOptions{SignInRequired: setting.Service.RequireSignInView})
	ignExploreSignIn := context.Toggle(&context.ToggleOptions{SignInRequired: setting.Service.RequireSignInView || setting.Service.Explore.RequireSigninView})
	ignSignInAndCsrf := context.Toggle(&context.ToggleOptions{DisableCSRF: true})
	reqSignOut := context.Toggle(&context.ToggleOptions{SignOutRequired: true})

	//bindIgnErr := binding.BindIgnErr
	bindIgnErr := web.Bind
	validation.AddBindingRules()

	openIDSignInEnabled := func(ctx *context.Context) {
		if !setting.Service.EnableOpenIDSignIn {
			ctx.Error(403)
			return
		}
	}

	openIDSignUpEnabled := func(ctx *context.Context) {
		if !setting.Service.EnableOpenIDSignUp {
			ctx.Error(403)
			return
		}
	}

	reqMilestonesDashboardPageEnabled := func(ctx *context.Context) {
		if !setting.Service.ShowMilestonesDashboardPage {
			ctx.Error(403)
			return
		}
	}

	// webhooksEnabled requires webhooks to be enabled by admin.
	webhooksEnabled := func(ctx *context.Context) {
		if setting.DisableWebhooks {
			ctx.Error(403)
			return
		}
	}

	// FIXME: not all routes need go through same middleware.
	// Especially some AJAX requests, we can reduce middleware number to improve performance.
	// Routers.
	// for health check
	m.Get("/", routers.Home)
	m.Group("/explore", func() {
		m.Get("", func(ctx *context.Context) {
			ctx.Redirect(setting.AppSubURL + "/explore/repos")
		})
		m.Get("/repos", routers.ExploreRepos)
		m.Get("/users", routers.ExploreUsers)
		m.Get("/organizations", routers.ExploreOrganizations)
		m.Get("/code", routers.ExploreCode)
	}, ignExploreSignIn)
	m.Get("/issues", reqSignIn, user.Issues)
	m.Get("/pulls", reqSignIn, user.Pulls)
	m.Get("/milestones", reqSignIn, reqMilestonesDashboardPageEnabled, user.Milestones)

	// ***** START: User *****
	m.Group("/user", func() {
		m.Get("/login", user.SignIn)
		m.Post("/login", bindIgnErr(auth.SignInForm{}), user.SignInPost)
		m.Group("", func() {
			m.Combo("/login/openid").
				Get(user.SignInOpenID).
				Post(bindIgnErr(auth.SignInOpenIDForm{}), user.SignInOpenIDPost)
		}, openIDSignInEnabled)
		m.Group("/openid", func() {
			m.Combo("/connect").
				Get(user.ConnectOpenID).
				Post(bindIgnErr(auth.ConnectOpenIDForm{}), user.ConnectOpenIDPost)
			m.Group("/register", func() {
				m.Combo("").
					Get(user.RegisterOpenID, openIDSignUpEnabled).
					Post(bindIgnErr(auth.SignUpOpenIDForm{}), user.RegisterOpenIDPost)
			}, openIDSignUpEnabled)
		}, openIDSignInEnabled)
		m.Get("/sign_up", user.SignUp)
		m.Post("/sign_up", bindIgnErr(auth.RegisterForm{}), user.SignUpPost)
		m.Group("/oauth2", func() {
			m.Get("/{provider}", user.SignInOAuth)
			m.Get("/{provider}/callback", user.SignInOAuthCallback)
		})
		m.Get("/link_account", user.LinkAccount)
		m.Post("/link_account_signin", bindIgnErr(auth.SignInForm{}), user.LinkAccountPostSignIn)
		m.Post("/link_account_signup", bindIgnErr(auth.RegisterForm{}), user.LinkAccountPostRegister)
		m.Group("/two_factor", func() {
			m.Get("", user.TwoFactor)
			m.Post("", bindIgnErr(auth.TwoFactorAuthForm{}), user.TwoFactorPost)
			m.Get("/scratch", user.TwoFactorScratch)
			m.Post("/scratch", bindIgnErr(auth.TwoFactorScratchAuthForm{}), user.TwoFactorScratchPost)
		})
		m.Group("/u2f", func() {
			m.Get("", user.U2F)
			m.Get("/challenge", user.U2FChallenge)
			m.Post("/sign", bindIgnErr(u2f.SignResponse{}), user.U2FSign)

		})
	}, reqSignOut)

	m.Any("/user/events", reqSignIn, events.Events)

	m.Group("/login/oauth", func() {
		m.Get("/authorize", bindIgnErr(auth.AuthorizationForm{}), user.AuthorizeOAuth)
		m.Post("/grant", bindIgnErr(auth.GrantApplicationForm{}), user.GrantApplicationOAuth)
		// TODO manage redirection
		m.Post("/authorize", bindIgnErr(auth.AuthorizationForm{}), user.AuthorizeOAuth)
	}, ignSignInAndCsrf, reqSignIn)
	if setting.CORSConfig.Enabled {
		m.Post("/login/oauth/access_token", cors.Handler(cors.Options{
			//Scheme:           setting.CORSConfig.Scheme, // FIXME: the cors middleware needs scheme option
			AllowedOrigins: setting.CORSConfig.AllowDomain,
			//setting.CORSConfig.AllowSubdomain // FIXME: the cors middleware needs allowSubdomain option
			AllowedMethods:   setting.CORSConfig.Methods,
			AllowCredentials: setting.CORSConfig.AllowCredentials,
			MaxAge:           int(setting.CORSConfig.MaxAge.Seconds()),
		}), bindIgnErr(auth.AccessTokenForm{}), ignSignInAndCsrf, user.AccessTokenOAuth)
	} else {
		m.Post("/login/oauth/access_token", bindIgnErr(auth.AccessTokenForm{}), ignSignInAndCsrf, user.AccessTokenOAuth)
	}

	m.Group("/user/settings", func() {
		m.Get("", userSetting.Profile)
		m.Post("", bindIgnErr(auth.UpdateProfileForm{}), userSetting.ProfilePost)
		m.Get("/change_password", user.MustChangePassword)
		m.Post("/change_password", bindIgnErr(auth.MustChangePasswordForm{}), user.MustChangePasswordPost)
		m.Post("/avatar", bindIgnErr(auth.AvatarForm{}), userSetting.AvatarPost)
		m.Post("/avatar/delete", userSetting.DeleteAvatar)
		m.Group("/account", func() {
			m.Combo("").Get(userSetting.Account).Post(bindIgnErr(auth.ChangePasswordForm{}), userSetting.AccountPost)
			m.Post("/email", bindIgnErr(auth.AddEmailForm{}), userSetting.EmailPost)
			m.Post("/email/delete", userSetting.DeleteEmail)
			m.Post("/delete", userSetting.DeleteAccount)
			m.Post("/theme", bindIgnErr(auth.UpdateThemeForm{}), userSetting.UpdateUIThemePost)
		})
		m.Group("/security", func() {
			m.Get("", userSetting.Security)
			m.Group("/two_factor", func() {
				m.Post("/regenerate_scratch", userSetting.RegenerateScratchTwoFactor)
				m.Post("/disable", userSetting.DisableTwoFactor)
				m.Get("/enroll", userSetting.EnrollTwoFactor)
				m.Post("/enroll", bindIgnErr(auth.TwoFactorAuthForm{}), userSetting.EnrollTwoFactorPost)
			})
			m.Group("/u2f", func() {
				m.Post("/request_register", bindIgnErr(auth.U2FRegistrationForm{}), userSetting.U2FRegister)
				m.Post("/register", bindIgnErr(u2f.RegisterResponse{}), userSetting.U2FRegisterPost)
				m.Post("/delete", bindIgnErr(auth.U2FDeleteForm{}), userSetting.U2FDelete)
			})
			m.Group("/openid", func() {
				m.Post("", bindIgnErr(auth.AddOpenIDForm{}), userSetting.OpenIDPost)
				m.Post("/delete", userSetting.DeleteOpenID)
				m.Post("/toggle_visibility", userSetting.ToggleOpenIDVisibility)
			}, openIDSignInEnabled)
			m.Post("/account_link", userSetting.DeleteAccountLink)
		})
		m.Group("/applications/oauth2", func() {
			m.Get("/{id}", userSetting.OAuth2ApplicationShow)
			m.Post("/{id}", bindIgnErr(auth.EditOAuth2ApplicationForm{}), userSetting.OAuthApplicationsEdit)
			m.Post("/{id}/regenerate_secret", userSetting.OAuthApplicationsRegenerateSecret)
			m.Post("", bindIgnErr(auth.EditOAuth2ApplicationForm{}), userSetting.OAuthApplicationsPost)
			m.Post("/delete", userSetting.DeleteOAuth2Application)
			m.Post("/revoke", userSetting.RevokeOAuth2Grant)
		})
		m.Combo("/applications").Get(userSetting.Applications).
			Post(bindIgnErr(auth.NewAccessTokenForm{}), userSetting.ApplicationsPost)
		m.Post("/applications/delete", userSetting.DeleteApplication)
		m.Combo("/keys").Get(userSetting.Keys).
			Post(bindIgnErr(auth.AddKeyForm{}), userSetting.KeysPost)
		m.Post("/keys/delete", userSetting.DeleteKey)
		m.Get("/organization", userSetting.Organization)
		m.Get("/repos", userSetting.Repos)
		m.Post("/repos/unadopted", userSetting.AdoptOrDeleteRepository)
	}, reqSignIn, func(ctx *context.Context) {
		ctx.Data["PageIsUserSettings"] = true
		ctx.Data["AllThemes"] = setting.UI.Themes
	})

	m.Group("/user", func() {
		// r.Get("/feeds", binding.Bind(auth.FeedsForm{}), user.Feeds)
		m.Any("/activate", user.Activate, reqSignIn)
		m.Any("/activate_email", user.ActivateEmail)
		m.Get("/avatar/{username}/{size}", user.Avatar)
		m.Get("/email2user", user.Email2User)
		m.Get("/recover_account", user.ResetPasswd)
		m.Post("/recover_account", user.ResetPasswdPost)
		m.Get("/forgot_password", user.ForgotPasswd)
		m.Post("/forgot_password", user.ForgotPasswdPost)
		m.Post("/logout", user.SignOut)
		m.Get("/task/{task}", user.TaskStatus)
	})
	// ***** END: User *****

	m.Get("/avatar/{hash}", user.AvatarByEmailHash)

	adminReq := context.Toggle(&context.ToggleOptions{SignInRequired: true, AdminRequired: true})

	// ***** START: Admin *****
	m.Group("/admin", func() {
		m.Get("", adminReq, admin.Dashboard)
		m.Post("", adminReq, bindIgnErr(auth.AdminDashboardForm{}), admin.DashboardPost)
		m.Get("/config", admin.Config)
		m.Post("/config/test_mail", admin.SendTestMail)
		m.Group("/monitor", func() {
			m.Get("", admin.Monitor)
			m.Post("/cancel/{pid}", admin.MonitorCancel)
			m.Group("/queue/{qid}", func() {
				m.Get("", admin.Queue)
				m.Post("/set", admin.SetQueueSettings)
				m.Post("/add", admin.AddWorkers)
				m.Post("/cancel/{pid}", admin.WorkerCancel)
				m.Post("/flush", admin.Flush)
			})
		})

		m.Group("/users", func() {
			m.Get("", admin.Users)
			m.Combo("/new").Get(admin.NewUser).Post(bindIgnErr(auth.AdminCreateUserForm{}), admin.NewUserPost)
			m.Combo("/{userid}").Get(admin.EditUser).Post(bindIgnErr(auth.AdminEditUserForm{}), admin.EditUserPost)
			m.Post("/{userid}/delete", admin.DeleteUser)
		})

		m.Group("/emails", func() {
			m.Get("", admin.Emails)
			m.Post("/activate", admin.ActivateEmail)
		})

		m.Group("/orgs", func() {
			m.Get("", admin.Organizations)
		})

		m.Group("/repos", func() {
			m.Get("", admin.Repos)
			m.Combo("/unadopted").Get(admin.UnadoptedRepos).Post(admin.AdoptOrDeleteRepository)
			m.Post("/delete", admin.DeleteRepo)
		})

		m.Group("/hooks", func() {
			m.Get("", admin.DefaultOrSystemWebhooks)
			m.Post("/delete", admin.DeleteDefaultOrSystemWebhook)
			m.Get("/{id}", repo.WebHooksEdit)
			m.Post("/gitea/{id}", bindIgnErr(auth.NewWebhookForm{}), repo.WebHooksEditPost)
			m.Post("/gogs/{id}", bindIgnErr(auth.NewGogshookForm{}), repo.GogsHooksEditPost)
			m.Post("/slack/{id}", bindIgnErr(auth.NewSlackHookForm{}), repo.SlackHooksEditPost)
			m.Post("/discord/{id}", bindIgnErr(auth.NewDiscordHookForm{}), repo.DiscordHooksEditPost)
			m.Post("/dingtalk/{id}", bindIgnErr(auth.NewDingtalkHookForm{}), repo.DingtalkHooksEditPost)
			m.Post("/telegram/{id}", bindIgnErr(auth.NewTelegramHookForm{}), repo.TelegramHooksEditPost)
			m.Post("/matrix/{id}", bindIgnErr(auth.NewMatrixHookForm{}), repo.MatrixHooksEditPost)
			m.Post("/msteams/{id}", bindIgnErr(auth.NewMSTeamsHookForm{}), repo.MSTeamsHooksEditPost)
			m.Post("/feishu/{id}", bindIgnErr(auth.NewFeishuHookForm{}), repo.FeishuHooksEditPost)
		}, webhooksEnabled)

		m.Group("/{configType:default-hooks|system-hooks}", func() {
			m.Get("/{type}/new", repo.WebhooksNew)
			m.Post("/gitea/new", bindIgnErr(auth.NewWebhookForm{}), repo.GiteaHooksNewPost)
			m.Post("/gogs/new", bindIgnErr(auth.NewGogshookForm{}), repo.GogsHooksNewPost)
			m.Post("/slack/new", bindIgnErr(auth.NewSlackHookForm{}), repo.SlackHooksNewPost)
			m.Post("/discord/new", bindIgnErr(auth.NewDiscordHookForm{}), repo.DiscordHooksNewPost)
			m.Post("/dingtalk/new", bindIgnErr(auth.NewDingtalkHookForm{}), repo.DingtalkHooksNewPost)
			m.Post("/telegram/new", bindIgnErr(auth.NewTelegramHookForm{}), repo.TelegramHooksNewPost)
			m.Post("/matrix/new", bindIgnErr(auth.NewMatrixHookForm{}), repo.MatrixHooksNewPost)
			m.Post("/msteams/new", bindIgnErr(auth.NewMSTeamsHookForm{}), repo.MSTeamsHooksNewPost)
			m.Post("/feishu/new", bindIgnErr(auth.NewFeishuHookForm{}), repo.FeishuHooksNewPost)
		})

		m.Group("/auths", func() {
			m.Get("", admin.Authentications)
			m.Combo("/new").Get(admin.NewAuthSource).Post(bindIgnErr(auth.AuthenticationForm{}), admin.NewAuthSourcePost)
			m.Combo("/{authid}").Get(admin.EditAuthSource).
				Post(bindIgnErr(auth.AuthenticationForm{}), admin.EditAuthSourcePost)
			m.Post("/{authid}/delete", admin.DeleteAuthSource)
		})

		m.Group("/notices", func() {
			m.Get("", admin.Notices)
			m.Post("/delete", admin.DeleteNotices)
			m.Post("/empty", admin.EmptyNotices)
		})
	}, adminReq)
	// ***** END: Admin *****

	m.Group("", func() {
		m.Get("/{username}", user.Profile)
		m.Get("/attachments/{uuid}", repo.GetAttachment)
	}, ignSignIn)

	m.Group("/{username}", func() {
		m.Post("/action/{action}", user.Action)
	}, reqSignIn)

	if !setting.IsProd() {
		m.Get("/template/*", dev.TemplatePreview)
	}

	reqRepoAdmin := context.RequireRepoAdmin()
	reqRepoCodeWriter := context.RequireRepoWriter(models.UnitTypeCode)
	reqRepoCodeReader := context.RequireRepoReader(models.UnitTypeCode)
	reqRepoReleaseWriter := context.RequireRepoWriter(models.UnitTypeReleases)
	reqRepoReleaseReader := context.RequireRepoReader(models.UnitTypeReleases)
	reqRepoWikiWriter := context.RequireRepoWriter(models.UnitTypeWiki)
	reqRepoIssueWriter := context.RequireRepoWriter(models.UnitTypeIssues)
	reqRepoIssueReader := context.RequireRepoReader(models.UnitTypeIssues)
	reqRepoPullsReader := context.RequireRepoReader(models.UnitTypePullRequests)
	reqRepoIssuesOrPullsWriter := context.RequireRepoWriterOr(models.UnitTypeIssues, models.UnitTypePullRequests)
	reqRepoIssuesOrPullsReader := context.RequireRepoReaderOr(models.UnitTypeIssues, models.UnitTypePullRequests)
	reqRepoProjectsReader := context.RequireRepoReader(models.UnitTypeProjects)
	reqRepoProjectsWriter := context.RequireRepoWriter(models.UnitTypeProjects)

	// ***** START: Organization *****
	m.Group("/org", func() {
		m.Group("", func() {
			m.Get("/create", org.Create)
			m.Post("/create", bindIgnErr(auth.CreateOrgForm{}), org.CreatePost)
		})

		m.Group("/{org}", func() {
			m.Get("/dashboard", user.Dashboard)
			m.Get("/dashboard/{team}", user.Dashboard)
			m.Get("/issues", user.Issues)
			m.Get("/issues/{team}", user.Issues)
			m.Get("/pulls", user.Pulls)
			m.Get("/pulls/{team}", user.Pulls)
			m.Get("/milestones", reqMilestonesDashboardPageEnabled, user.Milestones)
			m.Get("/milestones/{team}", reqMilestonesDashboardPageEnabled, user.Milestones)
			m.Get("/members", org.Members)
			m.Post("/members/action/{action}", org.MembersAction)
			m.Get("/teams", org.Teams)
		}, context.OrgAssignment(true, false, true))

		m.Group("/{org}", func() {
			m.Get("/teams/{team}", org.TeamMembers)
			m.Get("/teams/{team}/repositories", org.TeamRepositories)
			m.Post("/teams/{team}/action/{action}", org.TeamsAction)
			m.Post("/teams/{team}/action/repo/{action}", org.TeamsRepoAction)
		}, context.OrgAssignment(true, false, true))

		m.Group("/{org}", func() {
			m.Get("/teams/new", org.NewTeam)
			m.Post("/teams/new", bindIgnErr(auth.CreateTeamForm{}), org.NewTeamPost)
			m.Get("/teams/{team}/edit", org.EditTeam)
			m.Post("/teams/{team}/edit", bindIgnErr(auth.CreateTeamForm{}), org.EditTeamPost)
			m.Post("/teams/{team}/delete", org.DeleteTeam)

			m.Group("/settings", func() {
				m.Combo("").Get(org.Settings).
					Post(bindIgnErr(auth.UpdateOrgSettingForm{}), org.SettingsPost)
				m.Post("/avatar", bindIgnErr(auth.AvatarForm{}), org.SettingsAvatar)
				m.Post("/avatar/delete", org.SettingsDeleteAvatar)

				m.Group("/hooks", func() {
					m.Get("", org.Webhooks)
					m.Post("/delete", org.DeleteWebhook)
					m.Get("/{type}/new", repo.WebhooksNew)
					m.Post("/gitea/new", bindIgnErr(auth.NewWebhookForm{}), repo.GiteaHooksNewPost)
					m.Post("/gogs/new", bindIgnErr(auth.NewGogshookForm{}), repo.GogsHooksNewPost)
					m.Post("/slack/new", bindIgnErr(auth.NewSlackHookForm{}), repo.SlackHooksNewPost)
					m.Post("/discord/new", bindIgnErr(auth.NewDiscordHookForm{}), repo.DiscordHooksNewPost)
					m.Post("/dingtalk/new", bindIgnErr(auth.NewDingtalkHookForm{}), repo.DingtalkHooksNewPost)
					m.Post("/telegram/new", bindIgnErr(auth.NewTelegramHookForm{}), repo.TelegramHooksNewPost)
					m.Post("/matrix/new", bindIgnErr(auth.NewMatrixHookForm{}), repo.MatrixHooksNewPost)
					m.Post("/msteams/new", bindIgnErr(auth.NewMSTeamsHookForm{}), repo.MSTeamsHooksNewPost)
					m.Post("/feishu/new", bindIgnErr(auth.NewFeishuHookForm{}), repo.FeishuHooksNewPost)
					m.Get("/{id}", repo.WebHooksEdit)
					m.Post("/gitea/{id}", bindIgnErr(auth.NewWebhookForm{}), repo.WebHooksEditPost)
					m.Post("/gogs/{id}", bindIgnErr(auth.NewGogshookForm{}), repo.GogsHooksEditPost)
					m.Post("/slack/{id}", bindIgnErr(auth.NewSlackHookForm{}), repo.SlackHooksEditPost)
					m.Post("/discord/{id}", bindIgnErr(auth.NewDiscordHookForm{}), repo.DiscordHooksEditPost)
					m.Post("/dingtalk/{id}", bindIgnErr(auth.NewDingtalkHookForm{}), repo.DingtalkHooksEditPost)
					m.Post("/telegram/{id}", bindIgnErr(auth.NewTelegramHookForm{}), repo.TelegramHooksEditPost)
					m.Post("/matrix/{id}", bindIgnErr(auth.NewMatrixHookForm{}), repo.MatrixHooksEditPost)
					m.Post("/msteams/{id}", bindIgnErr(auth.NewMSTeamsHookForm{}), repo.MSTeamsHooksEditPost)
					m.Post("/feishu/{id}", bindIgnErr(auth.NewFeishuHookForm{}), repo.FeishuHooksEditPost)
				}, webhooksEnabled)

				m.Group("/labels", func() {
					m.Get("", org.RetrieveLabels, org.Labels)
					m.Post("/new", bindIgnErr(auth.CreateLabelForm{}), org.NewLabel)
					m.Post("/edit", bindIgnErr(auth.CreateLabelForm{}), org.UpdateLabel)
					m.Post("/delete", org.DeleteLabel)
					m.Post("/initialize", bindIgnErr(auth.InitializeLabelsForm{}), org.InitializeLabels)
				})

				m.Route("/delete", "GET,POST", org.SettingsDelete)
			})
		}, context.OrgAssignment(true, true))
	}, reqSignIn)
	// ***** END: Organization *****

	// ***** START: Repository *****
	m.Group("/repo", func() {
		m.Get("/create", repo.Create)
		m.Post("/create", bindIgnErr(auth.CreateRepoForm{}), repo.CreatePost)
		m.Get("/migrate", repo.Migrate)
		m.Post("/migrate", bindIgnErr(auth.MigrateRepoForm{}), repo.MigratePost)
		m.Group("/fork", func() {
			m.Combo("/{repoid}").Get(repo.Fork).
				Post(bindIgnErr(auth.CreateRepoForm{}), repo.ForkPost)
		}, context.RepoIDAssignment(), context.UnitTypes(), reqRepoCodeReader)
	}, reqSignIn)

	// ***** Release Attachment Download without Signin
	m.Get("/{username}/{reponame}/releases/download/{vTag}/{fileName}", ignSignIn, context.RepoAssignment(), repo.MustBeNotEmpty, repo.RedirectDownload)

	m.Group("/{username}/{reponame}", func() {
		m.Group("/settings", func() {
			m.Combo("").Get(repo.Settings).
				Post(bindIgnErr(auth.RepoSettingForm{}), repo.SettingsPost)
			m.Post("/avatar", bindIgnErr(auth.AvatarForm{}), repo.SettingsAvatar)
			m.Post("/avatar/delete", repo.SettingsDeleteAvatar)

			m.Group("/collaboration", func() {
				m.Combo("").Get(repo.Collaboration).Post(repo.CollaborationPost)
				m.Post("/access_mode", repo.ChangeCollaborationAccessMode)
				m.Post("/delete", repo.DeleteCollaboration)
				m.Group("/team", func() {
					m.Post("", repo.AddTeamPost)
					m.Post("/delete", repo.DeleteTeam)
				})
			})
			m.Group("/branches", func() {
				m.Combo("").Get(repo.ProtectedBranch).Post(repo.ProtectedBranchPost)
				m.Combo("/*").Get(repo.SettingsProtectedBranch).
					Post(bindIgnErr(auth.ProtectBranchForm{}), context.RepoMustNotBeArchived(), repo.SettingsProtectedBranchPost)
			}, repo.MustBeNotEmpty)

			m.Group("/hooks/git", func() {
				m.Get("", repo.GitHooks)
				m.Combo("/{name}").Get(repo.GitHooksEdit).
					Post(repo.GitHooksEditPost)
			}, context.GitHookService())

			m.Group("/hooks", func() {
				m.Get("", repo.Webhooks)
				m.Post("/delete", repo.DeleteWebhook)
				m.Get("/{type}/new", repo.WebhooksNew)
				m.Post("/gitea/new", bindIgnErr(auth.NewWebhookForm{}), repo.GiteaHooksNewPost)
				m.Post("/gogs/new", bindIgnErr(auth.NewGogshookForm{}), repo.GogsHooksNewPost)
				m.Post("/slack/new", bindIgnErr(auth.NewSlackHookForm{}), repo.SlackHooksNewPost)
				m.Post("/discord/new", bindIgnErr(auth.NewDiscordHookForm{}), repo.DiscordHooksNewPost)
				m.Post("/dingtalk/new", bindIgnErr(auth.NewDingtalkHookForm{}), repo.DingtalkHooksNewPost)
				m.Post("/telegram/new", bindIgnErr(auth.NewTelegramHookForm{}), repo.TelegramHooksNewPost)
				m.Post("/matrix/new", bindIgnErr(auth.NewMatrixHookForm{}), repo.MatrixHooksNewPost)
				m.Post("/msteams/new", bindIgnErr(auth.NewMSTeamsHookForm{}), repo.MSTeamsHooksNewPost)
				m.Post("/feishu/new", bindIgnErr(auth.NewFeishuHookForm{}), repo.FeishuHooksNewPost)
				m.Get("/{id}", repo.WebHooksEdit)
				m.Post("/{id}/test", repo.TestWebhook)
				m.Post("/gitea/{id}", bindIgnErr(auth.NewWebhookForm{}), repo.WebHooksEditPost)
				m.Post("/gogs/{id}", bindIgnErr(auth.NewGogshookForm{}), repo.GogsHooksEditPost)
				m.Post("/slack/{id}", bindIgnErr(auth.NewSlackHookForm{}), repo.SlackHooksEditPost)
				m.Post("/discord/{id}", bindIgnErr(auth.NewDiscordHookForm{}), repo.DiscordHooksEditPost)
				m.Post("/dingtalk/{id}", bindIgnErr(auth.NewDingtalkHookForm{}), repo.DingtalkHooksEditPost)
				m.Post("/telegram/{id}", bindIgnErr(auth.NewTelegramHookForm{}), repo.TelegramHooksEditPost)
				m.Post("/matrix/{id}", bindIgnErr(auth.NewMatrixHookForm{}), repo.MatrixHooksEditPost)
				m.Post("/msteams/{id}", bindIgnErr(auth.NewMSTeamsHookForm{}), repo.MSTeamsHooksEditPost)
				m.Post("/feishu/{id}", bindIgnErr(auth.NewFeishuHookForm{}), repo.FeishuHooksEditPost)
			}, webhooksEnabled)

			m.Group("/keys", func() {
				m.Combo("").Get(repo.DeployKeys).
					Post(bindIgnErr(auth.AddKeyForm{}), repo.DeployKeysPost)
				m.Post("/delete", repo.DeleteDeployKey)
			})

			m.Group("/lfs", func() {
				m.Get("/", repo.LFSFiles)
				m.Get("/show/{oid}", repo.LFSFileGet)
				m.Post("/delete/{oid}", repo.LFSDelete)
				m.Get("/pointers", repo.LFSPointerFiles)
				m.Post("/pointers/associate", repo.LFSAutoAssociate)
				m.Get("/find", repo.LFSFileFind)
				m.Group("/locks", func() {
					m.Get("/", repo.LFSLocks)
					m.Post("/", repo.LFSLockFile)
					m.Post("/{lid}/unlock", repo.LFSUnlock)
				})
			})

		}, func(ctx *context.Context) {
			ctx.Data["PageIsSettings"] = true
			ctx.Data["LFSStartServer"] = setting.LFS.StartServer
		})
	}, reqSignIn, context.RepoAssignment(), context.UnitTypes(), reqRepoAdmin, context.RepoRef())

	m.Post("/{username}/{reponame}/action/{action}", reqSignIn, context.RepoAssignment(), context.UnitTypes(), repo.Action)

	// Grouping for those endpoints not requiring authentication
	m.Group("/{username}/{reponame}", func() {
		m.Group("/milestone", func() {
			m.Get("/{id}", repo.MilestoneIssuesAndPulls)
		}, reqRepoIssuesOrPullsReader, context.RepoRef())
		m.Combo("/compare/*", repo.MustBeNotEmpty, reqRepoCodeReader, repo.SetEditorconfigIfExists).
			Get(ignSignIn, repo.SetDiffViewStyle, repo.SetWhitespaceBehavior, repo.CompareDiff).
			Post(reqSignIn, context.RepoMustNotBeArchived(), reqRepoPullsReader, repo.MustAllowPulls, bindIgnErr(auth.CreateIssueForm{}), repo.SetWhitespaceBehavior, repo.CompareAndPullRequestPost)
	}, context.RepoAssignment(), context.UnitTypes())

	// Grouping for those endpoints that do require authentication
	m.Group("/{username}/{reponame}", func() {
		m.Group("/issues", func() {
			m.Group("/new", func() {
				m.Combo("").Get(context.RepoRef(), repo.NewIssue).
					Post(bindIgnErr(auth.CreateIssueForm{}), repo.NewIssuePost)
				m.Get("/choose", context.RepoRef(), repo.NewIssueChooseTemplate)
			})
		}, context.RepoMustNotBeArchived(), reqRepoIssueReader)
		// FIXME: should use different URLs but mostly same logic for comments of issue and pull request.
		// So they can apply their own enable/disable logic on routers.
		m.Group("/issues", func() {
			m.Group("/{index}", func() {
				m.Post("/title", repo.UpdateIssueTitle)
				m.Post("/content", repo.UpdateIssueContent)
				m.Post("/watch", repo.IssueWatch)
				m.Post("/ref", repo.UpdateIssueRef)
				m.Group("/dependency", func() {
					m.Post("/add", repo.AddDependency)
					m.Post("/delete", repo.RemoveDependency)
				})
				m.Combo("/comments").Post(repo.MustAllowUserComment, bindIgnErr(auth.CreateCommentForm{}), repo.NewComment)
				m.Group("/times", func() {
					m.Post("/add", bindIgnErr(auth.AddTimeManuallyForm{}), repo.AddTimeManually)
					m.Post("/{timeid}/delete", repo.DeleteTime)
					m.Group("/stopwatch", func() {
						m.Post("/toggle", repo.IssueStopwatch)
						m.Post("/cancel", repo.CancelStopwatch)
					})
				})
				m.Post("/reactions/{action}", bindIgnErr(auth.ReactionForm{}), repo.ChangeIssueReaction)
				m.Post("/lock", reqRepoIssueWriter, bindIgnErr(auth.IssueLockForm{}), repo.LockIssue)
				m.Post("/unlock", reqRepoIssueWriter, repo.UnlockIssue)
			}, context.RepoMustNotBeArchived())
			m.Group("/{index}", func() {
				m.Get("/attachments", repo.GetIssueAttachments)
				m.Get("/attachments/{uuid}", repo.GetAttachment)
			})

			m.Post("/labels", reqRepoIssuesOrPullsWriter, repo.UpdateIssueLabel)
			m.Post("/milestone", reqRepoIssuesOrPullsWriter, repo.UpdateIssueMilestone)
			m.Post("/projects", reqRepoIssuesOrPullsWriter, repo.UpdateIssueProject)
			m.Post("/assignee", reqRepoIssuesOrPullsWriter, repo.UpdateIssueAssignee)
			m.Post("/request_review", reqRepoIssuesOrPullsReader, repo.UpdatePullReviewRequest)
			m.Post("/dismiss_review", reqRepoAdmin, bindIgnErr(auth.DismissReviewForm{}), repo.DismissReview)
			m.Post("/status", reqRepoIssuesOrPullsWriter, repo.UpdateIssueStatus)
			m.Post("/resolve_conversation", reqRepoIssuesOrPullsReader, repo.UpdateResolveConversation)
			m.Post("/attachments", repo.UploadIssueAttachment)
			m.Post("/attachments/remove", repo.DeleteAttachment)
		}, context.RepoMustNotBeArchived())
		m.Group("/comments/{id}", func() {
			m.Post("", repo.UpdateCommentContent)
			m.Post("/delete", repo.DeleteComment)
			m.Post("/reactions/{action}", bindIgnErr(auth.ReactionForm{}), repo.ChangeCommentReaction)
		}, context.RepoMustNotBeArchived())
		m.Group("/comments/{id}", func() {
			m.Get("/attachments", repo.GetCommentAttachments)
		})
		m.Group("/labels", func() {
			m.Post("/new", bindIgnErr(auth.CreateLabelForm{}), repo.NewLabel)
			m.Post("/edit", bindIgnErr(auth.CreateLabelForm{}), repo.UpdateLabel)
			m.Post("/delete", repo.DeleteLabel)
			m.Post("/initialize", bindIgnErr(auth.InitializeLabelsForm{}), repo.InitializeLabels)
		}, context.RepoMustNotBeArchived(), reqRepoIssuesOrPullsWriter, context.RepoRef())
		m.Group("/milestones", func() {
			m.Combo("/new").Get(repo.NewMilestone).
				Post(bindIgnErr(auth.CreateMilestoneForm{}), repo.NewMilestonePost)
			m.Get("/{id}/edit", repo.EditMilestone)
			m.Post("/{id}/edit", bindIgnErr(auth.CreateMilestoneForm{}), repo.EditMilestonePost)
			m.Post("/{id}/{action}", repo.ChangeMilestoneStatus)
			m.Post("/delete", repo.DeleteMilestone)
		}, context.RepoMustNotBeArchived(), reqRepoIssuesOrPullsWriter, context.RepoRef())
		m.Group("/pull", func() {
			m.Post("/{index}/target_branch", repo.UpdatePullRequestTarget)
		}, context.RepoMustNotBeArchived())

		m.Group("", func() {
			m.Group("", func() {
				m.Combo("/_edit/*").Get(repo.EditFile).
					Post(bindIgnErr(auth.EditRepoFileForm{}), repo.EditFilePost)
				m.Combo("/_new/*").Get(repo.NewFile).
					Post(bindIgnErr(auth.EditRepoFileForm{}), repo.NewFilePost)
				m.Post("/_preview/*", bindIgnErr(auth.EditPreviewDiffForm{}), repo.DiffPreviewPost)
				m.Combo("/_delete/*").Get(repo.DeleteFile).
					Post(bindIgnErr(auth.DeleteRepoFileForm{}), repo.DeleteFilePost)
				m.Combo("/_upload/*", repo.MustBeAbleToUpload).
					Get(repo.UploadFile).
					Post(bindIgnErr(auth.UploadRepoFileForm{}), repo.UploadFilePost)
			}, context.RepoRefByType(context.RepoRefBranch), repo.MustBeEditable)
			m.Group("", func() {
				m.Post("/upload-file", repo.UploadFileToServer)
				m.Post("/upload-remove", bindIgnErr(auth.RemoveUploadFileForm{}), repo.RemoveUploadFileFromServer)
			}, context.RepoRef(), repo.MustBeEditable, repo.MustBeAbleToUpload)
		}, context.RepoMustNotBeArchived(), reqRepoCodeWriter, repo.MustBeNotEmpty)

		m.Group("/branches", func() {
			m.Group("/_new", func() {
				m.Post("/branch/*", context.RepoRefByType(context.RepoRefBranch), repo.CreateBranch)
				m.Post("/tag/*", context.RepoRefByType(context.RepoRefTag), repo.CreateBranch)
				m.Post("/commit/*", context.RepoRefByType(context.RepoRefCommit), repo.CreateBranch)
			}, bindIgnErr(auth.NewBranchForm{}))
			m.Post("/delete", repo.DeleteBranchPost)
			m.Post("/restore", repo.RestoreBranchPost)
		}, context.RepoMustNotBeArchived(), reqRepoCodeWriter, repo.MustBeNotEmpty)

	}, reqSignIn, context.RepoAssignment(), context.UnitTypes())

	// Releases
	m.Group("/{username}/{reponame}", func() {
		m.Get("/tags", repo.TagsList, repo.MustBeNotEmpty,
			reqRepoCodeReader, context.RepoRefByType(context.RepoRefTag))
		m.Group("/releases", func() {
			m.Get("/", repo.Releases)
			m.Get("/tag/*", repo.SingleRelease)
			m.Get("/latest", repo.LatestRelease)
			m.Get("/attachments/{uuid}", repo.GetAttachment)
		}, repo.MustBeNotEmpty, reqRepoReleaseReader, context.RepoRefByType(context.RepoRefTag))
		m.Group("/releases", func() {
			m.Get("/new", repo.NewRelease)
			m.Post("/new", bindIgnErr(auth.NewReleaseForm{}), repo.NewReleasePost)
			m.Post("/delete", repo.DeleteRelease)
			m.Post("/attachments", repo.UploadReleaseAttachment)
			m.Post("/attachments/remove", repo.DeleteAttachment)
		}, reqSignIn, repo.MustBeNotEmpty, context.RepoMustNotBeArchived(), reqRepoReleaseWriter, context.RepoRef())
		m.Post("/tags/delete", repo.DeleteTag, reqSignIn,
			repo.MustBeNotEmpty, context.RepoMustNotBeArchived(), reqRepoCodeWriter, context.RepoRef())
		m.Group("/releases", func() {
			m.Get("/edit/*", repo.EditRelease)
			m.Post("/edit/*", bindIgnErr(auth.EditReleaseForm{}), repo.EditReleasePost)
		}, reqSignIn, repo.MustBeNotEmpty, context.RepoMustNotBeArchived(), reqRepoReleaseWriter, func(ctx *context.Context) {
			var err error
			ctx.Repo.Commit, err = ctx.Repo.GitRepo.GetBranchCommit(ctx.Repo.Repository.DefaultBranch)
			if err != nil {
				ctx.ServerError("GetBranchCommit", err)
				return
			}
			ctx.Repo.CommitsCount, err = ctx.Repo.GetCommitsCount()
			if err != nil {
				ctx.ServerError("GetCommitsCount", err)
				return
			}
			ctx.Data["CommitsCount"] = ctx.Repo.CommitsCount
		})
	}, ignSignIn, context.RepoAssignment(), context.UnitTypes(), reqRepoReleaseReader)

	m.Group("/{username}/{reponame}", func() {
		m.Post("/topics", repo.TopicsPost)
	}, context.RepoAssignment(), context.RepoMustNotBeArchived(), reqRepoAdmin)

	m.Group("/{username}/{reponame}", func() {
		m.Group("", func() {
			m.Get("/{type:issues|pulls}", repo.Issues)
			m.Get("/{type:issues|pulls}/{index}", repo.ViewIssue)
			m.Get("/labels", reqRepoIssuesOrPullsReader, repo.RetrieveLabels, repo.Labels)
			m.Get("/milestones", reqRepoIssuesOrPullsReader, repo.Milestones)
		}, context.RepoRef())

		m.Group("/projects", func() {
			m.Get("", repo.Projects)
			m.Get("/{id}", repo.ViewProject)
			m.Group("", func() {
				m.Get("/new", repo.NewProject)
				m.Post("/new", bindIgnErr(auth.CreateProjectForm{}), repo.NewProjectPost)
				m.Group("/{id}", func() {
					m.Post("", bindIgnErr(auth.EditProjectBoardForm{}), repo.AddBoardToProjectPost)
					m.Post("/delete", repo.DeleteProject)

					m.Get("/edit", repo.EditProject)
					m.Post("/edit", bindIgnErr(auth.CreateProjectForm{}), repo.EditProjectPost)
					m.Post("/{action:open|close}", repo.ChangeProjectStatus)

					m.Group("/{boardID}", func() {
						m.Put("", bindIgnErr(auth.EditProjectBoardForm{}), repo.EditProjectBoard)
						m.Delete("", repo.DeleteProjectBoard)
						m.Post("/default", repo.SetDefaultProjectBoard)

						m.Post("/{index}", repo.MoveIssueAcrossBoards)
					})
				})
			}, reqRepoProjectsWriter, context.RepoMustNotBeArchived())
		}, reqRepoProjectsReader, repo.MustEnableProjects)

		m.Group("/wiki", func() {
			m.Get("/", repo.Wiki)
			m.Get("/{page}", repo.Wiki)
			m.Get("/_pages", repo.WikiPages)
			m.Get("/{page}/_revision", repo.WikiRevision)
			m.Get("/commit/{sha:[a-f0-9]{7,40}}", repo.SetEditorconfigIfExists, repo.SetDiffViewStyle, repo.SetWhitespaceBehavior, repo.Diff)
			m.Get("/commit/{sha:[a-f0-9]{7,40}}.{:patch|diff}", repo.RawDiff)

			m.Group("", func() {
				m.Combo("/_new").Get(repo.NewWiki).
					Post(bindIgnErr(auth.NewWikiForm{}), repo.NewWikiPost)
				m.Combo("/{page}/_edit").Get(repo.EditWiki).
					Post(bindIgnErr(auth.NewWikiForm{}), repo.EditWikiPost)
				m.Post("/{page}/delete", repo.DeleteWikiPagePost)
			}, context.RepoMustNotBeArchived(), reqSignIn, reqRepoWikiWriter)
		}, repo.MustEnableWiki, context.RepoRef(), func(ctx *context.Context) {
			ctx.Data["PageIsWiki"] = true
		})

		m.Group("/wiki", func() {
			m.Get("/raw/*", repo.WikiRaw)
		}, repo.MustEnableWiki)

		m.Group("/activity", func() {
			m.Get("", repo.Activity)
			m.Get("/{period}", repo.Activity)
		}, context.RepoRef(), repo.MustBeNotEmpty, context.RequireRepoReaderOr(models.UnitTypePullRequests, models.UnitTypeIssues, models.UnitTypeReleases))

		m.Group("/activity_author_data", func() {
			m.Get("", repo.ActivityAuthors)
			m.Get("/{period}", repo.ActivityAuthors)
		}, context.RepoRef(), repo.MustBeNotEmpty, context.RequireRepoReaderOr(models.UnitTypeCode))

		m.Group("/archive", func() {
			m.Get("/*", repo.Download)
			m.Post("/*", repo.InitiateDownload)
		}, repo.MustBeNotEmpty, reqRepoCodeReader)

		m.Group("/branches", func() {
			m.Get("", repo.Branches)
		}, repo.MustBeNotEmpty, context.RepoRef(), reqRepoCodeReader)

		m.Group("/blob_excerpt", func() {
			m.Get("/{sha}", repo.SetEditorconfigIfExists, repo.SetDiffViewStyle, repo.ExcerptBlob)
		}, repo.MustBeNotEmpty, context.RepoRef(), reqRepoCodeReader)

		m.Group("/pulls/{index}", func() {
			m.Get(".diff", repo.DownloadPullDiff)
			m.Get(".patch", repo.DownloadPullPatch)
			m.Get("/commits", context.RepoRef(), repo.ViewPullCommits)
			m.Post("/merge", context.RepoMustNotBeArchived(), bindIgnErr(auth.MergePullRequestForm{}), repo.MergePullRequest)
			m.Post("/update", repo.UpdatePullRequest)
			m.Post("/cleanup", context.RepoMustNotBeArchived(), context.RepoRef(), repo.CleanUpPullRequest)
			m.Group("/files", func() {
				m.Get("", context.RepoRef(), repo.SetEditorconfigIfExists, repo.SetDiffViewStyle, repo.SetWhitespaceBehavior, repo.ViewPullFiles)
				m.Group("/reviews", func() {
					m.Get("/new_comment", repo.RenderNewCodeCommentForm)
					m.Post("/comments", bindIgnErr(auth.CodeCommentForm{}), repo.CreateCodeComment)
					m.Post("/submit", bindIgnErr(auth.SubmitReviewForm{}), repo.SubmitReview)
				}, context.RepoMustNotBeArchived())
			})
		}, repo.MustAllowPulls)

		m.Group("/media", func() {
			m.Get("/branch/*", context.RepoRefByType(context.RepoRefBranch), repo.SingleDownloadOrLFS)
			m.Get("/tag/*", context.RepoRefByType(context.RepoRefTag), repo.SingleDownloadOrLFS)
			m.Get("/commit/*", context.RepoRefByType(context.RepoRefCommit), repo.SingleDownloadOrLFS)
			m.Get("/blob/{sha}", context.RepoRefByType(context.RepoRefBlob), repo.DownloadByIDOrLFS)
			// "/*" route is deprecated, and kept for backward compatibility
			m.Get("/*", context.RepoRefByType(context.RepoRefLegacy), repo.SingleDownloadOrLFS)
		}, repo.MustBeNotEmpty, reqRepoCodeReader)

		m.Group("/raw", func() {
			m.Get("/branch/*", context.RepoRefByType(context.RepoRefBranch), repo.SingleDownload)
			m.Get("/tag/*", context.RepoRefByType(context.RepoRefTag), repo.SingleDownload)
			m.Get("/commit/*", context.RepoRefByType(context.RepoRefCommit), repo.SingleDownload)
			m.Get("/blob/{sha}", context.RepoRefByType(context.RepoRefBlob), repo.DownloadByID)
			// "/*" route is deprecated, and kept for backward compatibility
			m.Get("/*", context.RepoRefByType(context.RepoRefLegacy), repo.SingleDownload)
		}, repo.MustBeNotEmpty, reqRepoCodeReader)

		m.Group("/commits", func() {
			m.Get("/branch/*", context.RepoRefByType(context.RepoRefBranch), repo.RefCommits)
			m.Get("/tag/*", context.RepoRefByType(context.RepoRefTag), repo.RefCommits)
			m.Get("/commit/*", context.RepoRefByType(context.RepoRefCommit), repo.RefCommits)
			// "/*" route is deprecated, and kept for backward compatibility
			m.Get("/*", context.RepoRefByType(context.RepoRefLegacy), repo.RefCommits)
		}, repo.MustBeNotEmpty, reqRepoCodeReader)

		m.Group("/blame", func() {
			m.Get("/branch/*", context.RepoRefByType(context.RepoRefBranch), repo.RefBlame)
			m.Get("/tag/*", context.RepoRefByType(context.RepoRefTag), repo.RefBlame)
			m.Get("/commit/*", context.RepoRefByType(context.RepoRefCommit), repo.RefBlame)
		}, repo.MustBeNotEmpty, reqRepoCodeReader)

		m.Group("", func() {
			m.Get("/graph", repo.Graph)
			m.Get("/commit/{sha:([a-f0-9]{7,40})$}", repo.SetEditorconfigIfExists, repo.SetDiffViewStyle, repo.SetWhitespaceBehavior, repo.Diff)
		}, repo.MustBeNotEmpty, context.RepoRef(), reqRepoCodeReader)

		m.Group("/src", func() {
			m.Get("/branch/*", context.RepoRefByType(context.RepoRefBranch), repo.Home)
			m.Get("/tag/*", context.RepoRefByType(context.RepoRefTag), repo.Home)
			m.Get("/commit/*", context.RepoRefByType(context.RepoRefCommit), repo.Home)
			// "/*" route is deprecated, and kept for backward compatibility
			m.Get("/*", context.RepoRefByType(context.RepoRefLegacy), repo.Home)
		}, repo.SetEditorconfigIfExists)

		m.Group("", func() {
			m.Get("/forks", repo.Forks)
		}, context.RepoRef(), reqRepoCodeReader)
		m.Get("/commit/{sha:([a-f0-9]{7,40})}.{ext:patch|diff}",
			repo.MustBeNotEmpty, reqRepoCodeReader, repo.RawDiff)
	}, ignSignIn, context.RepoAssignment(), context.UnitTypes())
	m.Group("/{username}/{reponame}", func() {
		m.Get("/stars", repo.Stars)
		m.Get("/watchers", repo.Watchers)
		m.Get("/search", reqRepoCodeReader, repo.Search)
	}, ignSignIn, context.RepoAssignment(), context.RepoRef(), context.UnitTypes())

	m.Group("/{username}", func() {
		m.Group("/{reponame}", func() {
			m.Get("", repo.SetEditorconfigIfExists, repo.Home)
		}, goGet, ignSignIn, context.RepoAssignment(), context.RepoRef(), context.UnitTypes())

		m.Group("/{reponame}", func() {
			m.Group("/info/lfs", func() {
				m.Post("/objects/batch", lfs.BatchHandler)
				m.Get("/objects/{oid}/{filename}", lfs.ObjectOidHandler)
				m.Any("/objects/{oid}", lfs.ObjectOidHandler)
				m.Post("/objects", lfs.PostHandler)
				m.Post("/verify", lfs.VerifyHandler)
				m.Group("/locks", func() {
					m.Get("/", lfs.GetListLockHandler)
					m.Post("/", lfs.PostLockHandler)
					m.Post("/verify", lfs.VerifyLockHandler)
					m.Post("/{lid}/unlock", lfs.UnLockHandler)
				})
				m.Any("/*", func(ctx *context.Context) {
					ctx.NotFound("", nil)
				})
			}, ignSignInAndCsrf)

			m.Group("", func() {
				m.Post("/git-upload-pack", repo.ServiceUploadPack)
				m.Post("/git-receive-pack", repo.ServiceReceivePack)
				m.Get("/info/refs", repo.GetInfoRefs)
				m.Get("/HEAD", repo.GetTextFile("HEAD"))
				m.Get("/objects/info/alternates", repo.GetTextFile("objects/info/alternates"))
				m.Get("/objects/info/http-alternates", repo.GetTextFile("objects/info/http-alternates"))
				m.Get("/objects/info/packs", repo.GetInfoPacks)
				m.Get("/objects/info/{file:[^/]*}", repo.GetTextFile(""))
				m.Get("/objects/{head:[0-9a-f]{2}}/{hash:[0-9a-f]{38}}", repo.GetLooseObject)
				m.Get("/objects/pack/pack-{file:[0-9a-f]{40}}.pack", repo.GetPackFile)
				m.Get("/objects/pack/pack-{file:[0-9a-f]{40}}.idx", repo.GetIdxFile)
			}, ignSignInAndCsrf)

			m.Head("/tasks/trigger", repo.TriggerTask)
		})
	})
	// ***** END: Repository *****

	m.Group("/notifications", func() {
		m.Get("", user.Notifications)
		m.Post("/status", user.NotificationStatusPost)
		m.Post("/purge", user.NotificationPurgePost)
	}, reqSignIn)

	if setting.API.EnableSwagger {
		m.Get("/swagger.v1.json", routers.SwaggerV1Json)
	}

	// Not found handler.
	m.NotFound(web.Wrap(routers.NotFound))
}
