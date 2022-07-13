package authflow

import (
	"bufio"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"

	"github.com/cli/cli/v2/api"
	"github.com/cli/cli/v2/internal/ghinstance"
	"github.com/cli/cli/v2/pkg/cmdutil"
	"github.com/cli/cli/v2/pkg/iostreams"
	"github.com/cli/cli/v2/utils"
	"github.com/cli/oauth"
)

var (
	// The "GitHub CLI" OAuth app
	oauthClientID = "178c6fc778ccc68e1d6a"
	// This value is safe to be embedded in version control
	oauthClientSecret = "34ddeff2b558a23d38fba8a6de74f086ede1cc0b"
)

type iconfig interface {
	Get(string, string) (string, error)
	Set(string, string, string) error
	Write() error
	WriteHosts() error
}

func AuthFlowWithConfig(cfg iconfig, IO *iostreams.IOStreams, hostname, notice string, additionalScopes []string, isInteractive bool) (string, error) {
	// TODO this probably shouldn't live in this package. It should probably be in a new package that
	// depends on both iostreams and config.

	// FIXME: this duplicates `factory.browserLauncher()`
	browserLauncher := os.Getenv("GH_BROWSER")
	if browserLauncher == "" {
		browserLauncher, _ = cfg.Get("", "browser")
	}
	if browserLauncher == "" {
		browserLauncher = os.Getenv("BROWSER")
	}

	token, userLogin, err := authFlow(hostname, IO, notice, additionalScopes, isInteractive, browserLauncher)
	if err != nil {
		return "", err
	}

	err = cfg.Set(hostname, "user", userLogin)
	if err != nil {
		return "", err
	}
	err = cfg.Set(hostname, "oauth_token", token)
	if err != nil {
		return "", err
	}

	return token, cfg.WriteHosts()
}

func authFlow(oauthHost string, IO *iostreams.IOStreams, notice string, additionalScopes []string, isInteractive bool, browserLauncher string) (string, string, error) {
	w := IO.ErrOut
	cs := IO.ColorScheme()

	httpClient := http.DefaultClient
	debugEnabled, debugValue := utils.IsDebugEnabled()
	if debugEnabled {
		logTraffic := strings.Contains(debugValue, "api")
		httpClient.Transport = api.VerboseLog(IO.ErrOut, logTraffic, IO.ColorEnabled())(httpClient.Transport)
	}

	minimumScopes := []string{"repo", "read:org", "gist"}
	scopes := append(minimumScopes, additionalScopes...)

	callbackURI := "http://127.0.0.1/callback"
	if ghinstance.IsEnterprise(oauthHost) {
		// the OAuth app on Enterprise hosts is still registered with a legacy callback URL
		// see https://github.com/cli/cli/pull/222, https://github.com/cli/cli/pull/650
		callbackURI = "http://localhost/"
	}

	flow := &oauth.Flow{
		Host:         oauth.GitHubHost(ghinstance.HostPrefix(oauthHost)),
		ClientID:     oauthClientID,
		ClientSecret: oauthClientSecret,
		CallbackURI:  callbackURI,
		Scopes:       scopes,
		DisplayCode: func(code, verificationURL string) error {
			fmt.Fprintf(w, "%s First copy your one-time code: %s\n", cs.Yellow("!"), cs.Bold(code))
			return nil
		},
		BrowseURL: func(authURL string) error {
			if u, err := url.Parse(authURL); err == nil {
				if u.Scheme != "http" && u.Scheme != "https" {
					return fmt.Errorf("invalid URL: %s", authURL)
				}
			} else {
				return err
			}

			if !isInteractive {
				fmt.Fprintf(w, "%s to continue in your web browser: %s\n", cs.Bold("Open this URL"), authURL)
				return nil
			}

			fmt.Fprintf(w, "%s to open %s in your browser... ", cs.Bold("Press Enter"), oauthHost)
			_ = waitForEnter(IO.In)

			browser := cmdutil.NewBrowser(browserLauncher, IO.Out, IO.ErrOut)
			if err := browser.Browse(authURL); err != nil {
				fmt.Fprintf(w, "%s Failed opening a web browser at %s\n", cs.Red("!"), authURL)
				fmt.Fprintf(w, "  %s\n", err)
				fmt.Fprint(w, "  Please try entering the URL in your browser manually\n")
			}
			return nil
		},
		WriteSuccessHTML: func(w io.Writer) {
			fmt.Fprint(w, oauthSuccessPage)
		},
		HTTPClient: httpClient,
		Stdin:      IO.In,
		Stdout:     w,
	}

	fmt.Fprintln(w, notice)

	token, err := flow.DetectFlow()
	if err != nil {
		return "", "", err
	}

	userLogin, err := getViewer(oauthHost, token.Token)
	if err != nil {
		return "", "", err
	}

	return token.Token, userLogin, nil
}

func getViewer(hostname, token string) (string, error) {
	http := api.NewClient(api.AddHeader("Authorization", fmt.Sprintf("token %s", token)))
	return api.CurrentLoginName(http, hostname)
}

func waitForEnter(r io.Reader) error {
	scanner := bufio.NewScanner(r)
	scanner.Scan()
	return scanner.Err()
}
