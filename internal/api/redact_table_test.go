package api

import "testing"

// A reviewer's table of Env key names for SecretKey (marvel#784, 219 unique
// names), each with a verdict. must-redact names are secret-looking and must
// print as (redacted); must-show names are ordinary and must print as declared.
// accepted-either names are listed and not asserted: a reasonable reader could
// go either way, so the rule may change its mind about them without a test
// breaking. A rule change that flips a must-redact or must-show name is a
// change to what the operator's ruling (c) redacts, and belongs in review.

var reviewerMustRedact = []string{
	"PASSCODE", "STRIPE_SK", "JWT", "tokenizerKey", "PW_HASH", "clientKey",
	"githubPat", "ghPat", "authorizationHeader", "stripeKey", "masterKey", "licenseKey",
	"jwtKey", "hmacKey", "deployKey", "appKey", "npmPat", "gitlabPat",
	"AUTHORIZATIONHEADER", "proxyAuthorization", "ProxyAuthorization", "AuthenticationKey", "AUTHENTICATIONKEY", "OAuthClientKey",
	"FOO_TOKEN", "MY_SECRET", "FOO_KEY", "PASSWORD", "PASSWD", "MYSQL_PWD",
	"DB_PASS", "SMTP_PASS", "DB_PW", "FOO_CREDENTIAL", "FOO_CREDENTIALS", "API_KEY",
	"ACCESS_KEY", "AWS_SECRET_ACCESS_KEY", "AWS_SESSION_TOKEN", "AWS_BEARER_TOKEN_BEDROCK", "GITHUB_TOKEN", "GH_TOKEN",
	"ANTHROPIC_API_KEY", "FOO_AUTH", "PRIVATE_KEY", "SLACK_WEBHOOK_URL", "SSHKEY", "SIGNINGKEY",
	"CLIENT_SECRET", "NPM_TOKEN", "PGPASSWORD", "GITHUB_PAT", "OPENAI_API_KEY", "GH_AUTH",
	"AUTH_TOKEN", "X_AUTH", "BEARER_TOKEN", "ACCESSTOKEN", "APITOKEN", "REFRESHTOKEN",
	"TOKEN", "SLACK_TOKEN", "DOCKER_AUTH_CONFIG", "accessToken", "githubToken", "authToken",
	"basicAuth", "proxyAuth", "ghAuth", "BASICAUTH", "HTTPAUTH", "PROXYAUTH",
	"GITHUBAUTH", "AUTHKEY", "AUTHHEADER", "authHeader", "tokenValue", "TOKENVALUE",
	"APITOKEN2", "GITHUB_TOKEN2", "githubTokenV2", "TOKEN2", "AUTH2", "OAUTH2_TOKEN",
	"HTTP_AUTHORIZATION", "AUTHORIZATION", "PASS", "NETRC_PASS", "JWT_SECRET", "SESSION_COOKIE",
	"DB_PASSWD", "MY_PASSWORD_HASH", "PASSPHRASE", "SECRET", "CLIENT_SECRET_V2", "OAUTH_TOKEN",
	"OAUTH_CLIENT_SECRET", "GOOGLE_OAUTH_ACCESS_TOKEN", "AUTHOR_TOKEN", "PASS_KEY", "DB_PASS_PROD", "DB_PASS_1",
	"SMTP_PASS_2", "DB_PW_PROD", "MYSQL_PWD_PROD", "MYSQL_PWD_1", "DB_PASS2", "dbPass",
	"dbPassProd", "smtpPw", "mysqlPwd", "PASSWORDS", "OAuthToken", "OAUTHTOKEN",
	"OAuth2Token", "K8S_TOKEN", "OIDC_TOKEN", "SMTP_PASSWORD_2", "PW", "GH_PAT_2",
	"apiKey", "privateKey", "secretKey", "sshKey", "signingKey", "accessKey",
	"awsSecretAccessKey", "encryptionKey", "personalAccessToken", "Authorization", "X_AUTHORIZATION", "APIKEY",
	"PRIVATEKEY", "OAuthClientSecret", "OAuthAccessToken", "GoogleOAuthRefreshToken", "webhookKey", "AUTHENTICATION_TOKEN",
	"authenticationSecret", "SMTPPass", "ACCESSTOKENS", "dbPassword", "DB_PWD", "FOO_O_AUTH_TOKEN",
}

var reviewerMustShow = []string{
	"PWD", "OAuthClientID", "OAuthRedirectURI", "oauthRedirectUri", "CertAuthority", "AUTHORITY_URL",
	"AUTHENTICATION_ENABLED", "tokenizer", "Tokenizers", "PASS_THROUGH", "PASSTHROUGH", "S3_BUCKET",
	"IPv6Enabled", "HTTP2", "PATCH_LEVEL", "BYPASS", "COMPASS", "OAUTH_REDIRECT_URI",
	"GoogleOAuthClientID", "oAuthClientId", "PATH", "HOME", "KEYBOARD", "MONKEY",
	"REGION", "TOKENIZERS_PARALLELISM", "AUTHOR_NAME", "BYPASS_CACHE", "COMPASS_DIR", "PASSENGER_COUNT",
	"OLDPWD", "PW_DEBUG",
}

// reviewerAcceptedEither are not asserted.
var reviewerAcceptedEither = []string{
	"DSN", "SENTRY_DSN", "DATABASE_URL", "REDIS_URL", "MONGODB_URI", "BASIC_AUTH_USER",
	"CONN_STR", "CONNECTION_STRING", "AMQP_URL", "POSTGRES_URL", "PG_URL", "DB_URI",
	"MYSQL_URL", "PASSWORD_FILE", "PASS_FILE", "GOOGLEOAUTH", "AUTH_URL", "AUTH_ENABLED",
	"TOKEN_URL", "TOKENS", "MAX_TOKENS", "maxTokens", "TOKEN_LIMIT", "TOKEN_COUNT",
	"hotKey", "sortKey", "cacheKey", "primaryKey", "KEY_ID", "patCount",
	"DB_PASS_FILE", "HOT_KEY", "CACHE_KEY", "SORT_KEY", "PRIMARY_KEY", "PARTITION_KEY",
	"KEY_NAME", "PAT_COUNT", "PWD_FILE", "OAUTHAuth", "O_AUTH", "SESSION_ID",
	"GOOGLE_APPLICATION_CREDENTIALS",
}

func TestSecretKeyReviewerTableMustRedact(t *testing.T) {
	for _, name := range reviewerMustRedact {
		if !SecretKey(name) {
			t.Errorf("SecretKey(%q) = false, want true (must-redact)", name)
		}
	}
}

func TestSecretKeyReviewerTableMustShow(t *testing.T) {
	for _, name := range reviewerMustShow {
		if SecretKey(name) {
			t.Errorf("SecretKey(%q) = true, want false (must-show)", name)
		}
	}
}

// The three lists are disjoint and the table is the size it was reviewed at, so
// an edit cannot quietly move a name between them.
func TestSecretKeyReviewerTableShape(t *testing.T) {
	seen := map[string]string{}
	for list, names := range map[string][]string{
		"must-redact": reviewerMustRedact, "must-show": reviewerMustShow, "accepted-either": reviewerAcceptedEither,
	} {
		for _, n := range names {
			if prev, dup := seen[n]; dup {
				t.Errorf("%q is in both %s and %s", n, prev, list)
			}
			seen[n] = list
		}
	}
	if len(reviewerMustRedact) != 144 || len(reviewerMustShow) != 32 || len(reviewerAcceptedEither) != 43 {
		t.Errorf("table sizes = %d, %d, %d; want 144, 32, 43",
			len(reviewerMustRedact), len(reviewerMustShow), len(reviewerAcceptedEither))
	}
}
