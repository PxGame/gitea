module code.gitea.io/gitea

go 1.14

require (
	cloud.google.com/go v0.78.0 // indirect
	code.gitea.io/gitea-vet v0.2.1
	code.gitea.io/sdk/gitea v0.13.2
	gitea.com/go-chi/binding v0.0.0-20210301195521-1fe1c9a555e7
	gitea.com/go-chi/cache v0.0.0-20210110083709-82c4c9ce2d5e
	gitea.com/go-chi/captcha v0.0.0-20210110083842-e7696c336a1e
	gitea.com/go-chi/session v0.0.0-20210108030337-0cb48c5ba8ee
	gitea.com/lunny/levelqueue v0.3.0
	github.com/Microsoft/go-winio v0.4.16 // indirect
	github.com/NYTimes/gziphandler v1.1.1
	github.com/PuerkitoBio/goquery v1.5.1
	github.com/RoaringBitmap/roaring v0.5.5 // indirect
	github.com/alecthomas/chroma v0.8.2
	github.com/andybalholm/brotli v1.0.1 // indirect
	github.com/anmitsu/go-shlex v0.0.0-20200514113438-38f4b401e2be // indirect
	github.com/blevesearch/bleve/v2 v2.0.2
	github.com/boombuler/barcode v1.0.1 // indirect
	github.com/bradfitz/gomemcache v0.0.0-20190913173617-a41fca850d0b // indirect
	github.com/caddyserver/certmagic v0.12.0
	github.com/chi-middleware/proxy v1.1.1
	github.com/couchbase/go-couchbase v0.0.0-20210224140812-5740cd35f448 // indirect
	github.com/couchbase/gomemcached v0.1.2 // indirect
	github.com/couchbase/goutils v0.0.0-20210118111533-e33d3ffb5401 // indirect
	github.com/cpuguy83/go-md2man/v2 v2.0.0 // indirect
	github.com/denisenkom/go-mssqldb v0.9.0
	github.com/dgrijalva/jwt-go v3.2.0+incompatible
	github.com/dlclark/regexp2 v1.4.0 // indirect
	github.com/dustin/go-humanize v1.0.0
	github.com/editorconfig/editorconfig-core-go/v2 v2.4.1
	github.com/emirpasic/gods v1.12.0
	github.com/ethantkoenig/rupture v1.0.0
	github.com/gliderlabs/ssh v0.3.2
	github.com/glycerine/go-unsnap-stream v0.0.0-20210130063903-47dfef350d96 // indirect
	github.com/go-asn1-ber/asn1-ber v1.5.3 // indirect
	github.com/go-chi/chi v1.5.4
	github.com/go-chi/cors v1.1.1
	github.com/go-enry/go-enry/v2 v2.6.1
	github.com/go-git/go-billy/v5 v5.0.0
	github.com/go-git/go-git/v5 v5.2.0
	github.com/go-ldap/ldap/v3 v3.2.4
	github.com/go-openapi/errors v0.20.0 // indirect
	github.com/go-openapi/validate v0.20.2 // indirect
	github.com/go-redis/redis/v8 v8.6.0
	github.com/go-sql-driver/mysql v1.5.0
	github.com/go-swagger/go-swagger v0.26.1
	github.com/go-testfixtures/testfixtures/v3 v3.5.0
	github.com/gobwas/glob v0.2.3
	github.com/gogs/chardet v0.0.0-20191104214054-4b6791f73a28
	github.com/gogs/cron v0.0.0-20171120032916-9f6c956d3e14
	github.com/gogs/go-gogs-client v0.0.0-20210131175652-1d7215cd8d85
	github.com/golang/snappy v0.0.3 // indirect
	github.com/google/go-github/v32 v32.1.0
	github.com/google/uuid v1.2.0
	github.com/gorilla/context v1.1.1
	github.com/gorilla/mux v1.8.0 // indirect
	github.com/gorilla/sessions v1.2.1 // indirect
	github.com/hashicorp/go-cleanhttp v0.5.2 // indirect
	github.com/hashicorp/go-retryablehttp v0.6.8 // indirect
	github.com/hashicorp/go-version v1.2.1
	github.com/huandu/xstrings v1.3.2
	github.com/imdario/mergo v0.3.11 // indirect
	github.com/issue9/assert v1.3.2 // indirect
	github.com/issue9/identicon v1.0.1
	github.com/jaytaylor/html2text v0.0.0-20200412013138-3577fbdbcff7
	github.com/json-iterator/go v1.1.10
	github.com/kballard/go-shellquote v0.0.0-20180428030007-95032a82bc51
	github.com/kevinburke/ssh_config v0.0.0-20201106050909-4977a11b4351 // indirect
	github.com/keybase/go-crypto v0.0.0-20200123153347-de78d2cb44f4
	github.com/klauspost/compress v1.11.8
	github.com/klauspost/pgzip v1.2.5 // indirect
	github.com/lafriks/xormstore v1.4.0
	github.com/lib/pq v1.9.0
	github.com/libdns/libdns v0.2.0 // indirect
	github.com/lunny/dingtalk_webhook v0.0.0-20171025031554-e3534c89ef96
	github.com/mailru/easyjson v0.7.7 // indirect
	github.com/markbates/goth v1.67.1
	github.com/mattn/go-isatty v0.0.12
	github.com/mattn/go-runewidth v0.0.10 // indirect
	github.com/mattn/go-sqlite3 v1.14.6
	github.com/mgechev/dots v0.0.0-20190921121421-c36f7dcfbb81
	github.com/mgechev/revive v1.0.3
	github.com/mholt/acmez v0.1.3 // indirect
	github.com/mholt/archiver/v3 v3.5.0
	github.com/microcosm-cc/bluemonday v1.0.4
	github.com/miekg/dns v1.1.40 // indirect
	github.com/minio/md5-simd v1.1.2 // indirect
	github.com/minio/minio-go/v7 v7.0.10
	github.com/minio/sha256-simd v1.0.0 // indirect
	github.com/mitchellh/go-homedir v1.1.0
	github.com/mrjones/oauth v0.0.0-20190623134757-126b35219450 // indirect
	github.com/msteinert/pam v0.0.0-20201130170657-e61372126161
	github.com/nfnt/resize v0.0.0-20180221191011-83c6a9932646
	github.com/niklasfasching/go-org v1.4.0
	github.com/olekukonko/tablewriter v0.0.5 // indirect
	github.com/oliamb/cutter v0.2.2
	github.com/olivere/elastic/v7 v7.0.22
	github.com/pelletier/go-toml v1.8.1
	github.com/pierrec/lz4/v4 v4.1.3 // indirect
	github.com/pkg/errors v0.9.1
	github.com/pquerna/otp v1.3.0
	github.com/prometheus/client_golang v1.9.0
	github.com/prometheus/common v0.18.0 // indirect
	github.com/prometheus/procfs v0.6.0 // indirect
	github.com/quasoft/websspi v1.0.0
	github.com/rivo/uniseg v0.2.0 // indirect
	github.com/russross/blackfriday/v2 v2.1.0 // indirect
	github.com/sergi/go-diff v1.1.0
	github.com/shurcooL/httpfs v0.0.0-20190707220628-8d4bc4ba7749 // indirect
	github.com/shurcooL/vfsgen v0.0.0-20200824052919-0d455de96546
	github.com/spf13/afero v1.5.1 // indirect
	github.com/ssor/bom v0.0.0-20170718123548-6386211fdfcf // indirect
	github.com/stretchr/testify v1.7.0
	github.com/syndtr/goleveldb v1.0.0
	github.com/tinylib/msgp v1.1.5 // indirect
	github.com/tstranex/u2f v1.0.0
	github.com/ulikunitz/xz v0.5.10 // indirect
	github.com/unknwon/com v1.0.1
	github.com/unknwon/i18n v0.0.0-20200823051745-09abd91c7f2c
	github.com/unknwon/paginater v0.0.0-20200328080006-042474bd0eae
	github.com/unrolled/render v1.0.3
	github.com/urfave/cli v1.22.5
	github.com/willf/bitset v1.1.11 // indirect
	github.com/xanzy/go-gitlab v0.44.0
	github.com/xanzy/ssh-agent v0.3.0 // indirect
	github.com/yohcop/openid-go v1.0.0
	github.com/yuin/goldmark v1.3.2
	github.com/yuin/goldmark-highlighting v0.0.0-20200307114337-60d527fdb691
	github.com/yuin/goldmark-meta v1.0.0
	go.jolheiser.com/hcaptcha v0.0.4
	go.jolheiser.com/pwn v0.0.3
	go.uber.org/multierr v1.6.0 // indirect
	go.uber.org/zap v1.16.0 // indirect
	golang.org/x/crypto v0.0.0-20210220033148-5ea612d1eb83
	golang.org/x/net v0.0.0-20210226172049-e18ecbb05110
	golang.org/x/oauth2 v0.0.0-20210220000619-9bb904979d93
	golang.org/x/sys v0.0.0-20210228012217-479acdf4ea46
	golang.org/x/text v0.3.5
	golang.org/x/time v0.0.0-20210220033141-f8bda1e9f3ba // indirect
	golang.org/x/tools v0.1.0
	gopkg.in/alexcesaro/quotedprintable.v3 v3.0.0-20150716171945-2caba252f4dc // indirect
	gopkg.in/gomail.v2 v2.0.0-20160411212932-81ebce5c23df
	gopkg.in/ini.v1 v1.62.0
	gopkg.in/yaml.v2 v2.4.0
	mvdan.cc/xurls/v2 v2.2.0
	strk.kbt.io/projects/go/libravatar v0.0.0-20191008002943-06d1c002b251
	xorm.io/builder v0.3.9
	xorm.io/xorm v1.0.7
)

replace github.com/hashicorp/go-version => github.com/6543/go-version v1.2.4

replace github.com/microcosm-cc/bluemonday => github.com/lunny/bluemonday v1.0.5-0.20201227154428-ca34796141e8
