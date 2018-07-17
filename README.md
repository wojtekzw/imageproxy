# imageproxy [![Build Status](https://travis-ci.org/wojtekzw/imageproxy.svg?branch=master)](https://travis-ci.org/wojtekzw/imageproxy) [![GoDoc](https://godoc.org/willnorris.com/go/imageproxy?status.svg)](https://godoc.org/willnorris.com/go/imageproxy) [![Apache 2.0 License](https://img.shields.io/badge/license-Apache%202.0-blue.svg?style=flat)](LICENSE)

[original]: https://github.com/willnorris/imageproxy
[https://github.com/willnorris/imageproxy]: https://github.com/willnorris/imageproxy

imageproxy is a caching image proxy server written in Go.  It is the fork of
[https://github.com/willnorris/imageproxy]. This README comes in over 90% from [original] with some changes to accommodate
for feature changes in this fork. It features:

- basic image adjustments like resizing, cropping, and rotation
- access control using host or IP whitelists or request signing (HMAC-SHA256)
- support for jpeg, png, webp (decode only), tiff, and gif image formats (including animated gifs)
- on-disk caching, respecting the cache headers of the original images
- easy deployment, since it's pure go

Originaly it is used by its primarily author to dynamically resize images hosted on my his
site (read more in [this post][]).  But you can also enable request signing and
use it as an SSL proxy for remote images, similar to [atmos/camo][] but with
additional image adjustment options.

[this post]: https://willnorris.com/2014/01/a-self-hosted-alternative-to-jetpacks-photon-service
[atmos/camo]: https://github.com/atmos/camo

## URL Structure

imageproxy URLs are of the form `http://localhost/{options}/{remote_url}`.

### Options

Options are available for cropping, resizing, rotation, flipping, and digital
signatures among a few others.  Options for are specified as a comma delimited
list of parameters, which can be supplied in any order.  Duplicate parameters
overwrite previous values.

The format is a superset of [resize.ly's options](https://resize.ly/#demo).

#### Size

The size option takes the general form `{width}x{height}`, where width and
height are numbers.  Integer values greater than 1 are interpreted as exact
pixel values.  Floats between 0 and 1 are interpreted as percentages of the
original image size.  If either value is omitted or set to 0, it will be
automatically set to preserve the aspect ratio based on the other dimension.
If a single number is provided (with no "x" separator), it will be used for
both height and width.

#### Crop Mode

Depending on the options specified, an image may be cropped to fit the
requested size.  In all cases, the original aspect ratio of the image will be
preserved; imageproxy will never stretch the original image.

When no explicit crop mode is specified, the following rules are followed:

- If both width and height values are specified, the image will be scaled to
   fill the space, cropping if necessary to fit the exact dimension.
- If only one of the width or height values is specified, the image will be
   resized to fit the specified dimension, scaling the other dimension as
   needed to maintain the aspect ratio.

If the `fit` option is specified together with a width and height value, the
image will be resized to fit within a containing box of the specified size.  As
always, the original aspect ratio will be preserved. Specifying the `fit`
option with only one of either width or height does the same thing as if `fit`
had not been specified.

#### Absolute crop mode

Starting point can be added (top,left) `cx{start_x},cy{start_y` eg. cx10,cy20 - that means start crop from (10,20)
in original image. And size of the crop can be set `cw{width},ch{height}` eg. cw100,ch200 - that means width=100, height=200.
After absolute crop all other transformations are applied to the new cropped image.

#### Rotate

The `r{degrees}` option will rotate the image the specified number of degrees,
counter-clockwise.  Valid degrees values are `90`, `180`, and `270`.  Images
are rotated **after** being resized.

#### Flip

The `fv` option will flip the image vertically.  The `fh` option will flip the
image horizontally.  Images are flipped **after** being resized and rotated.

#### Quality

The `q{percentage}` option can be used to specify the output quality (JPEG
only).  If not specified, the default value of `95` is used.

#### Signature

The `s{signature}` option specifies an optional base64 encoded HMAC used to
sign the remote URL in the request.  The HMAC key used to verify signatures is
provided to the imageproxy server on startup.

See [URL Signing](https://github.com/willnorris/imageproxy/wiki/URL-signing)
for examples of generating signatures.

#### All options

See the full list of available options at
<https://godoc.org/willnorris.com/go/imageproxy#ParseOptions>.

### Remote URL

The URL of the original image to load is specified as the remainder of the
path, without any encoding.  For example,
`http://localhost/200/https://willnorris.com/logo.jpg`.

In order to [optimize caching][], it is recommended that URLs not contain query
strings.

[optimize caching]: http://www.stevesouders.com/blog/2008/08/23/revving-filenames-dont-use-querystring/

### Examples

The following live examples demonstrate setting different options on [this
source image][small-things], which measures 1024 by 678 pixels.

[small-things]: https://willnorris.com/2013/12/small-things.jpg

<!-- markdownlint-disable MD033 -->

Options | Meaning                                  | Image
--------|------------------------------------------|------
200x    | 200px wide, proportional height          | <a href="https://willnorris.com/api/imageproxy/200x/https://willnorris.com/2013/12/small-things.jpg"><img src="https://willnorris.com/api/imageproxy/200x/https://willnorris.com/2013/12/small-things.jpg" alt="200x"></a>
x0.15   | 15% original height, proportional width  | <a href="https://willnorris.com/api/imageproxy/x0.15/https://willnorris.com/2013/12/small-things.jpg"><img src="https://willnorris.com/api/imageproxy/x0.15/https://willnorris.com/2013/12/small-things.jpg" alt="x0.15"></a>
100x150 | 100 by 150 pixels, cropping as needed    | <a href="https://willnorris.com/api/imageproxy/100x150/https://willnorris.com/2013/12/small-things.jpg"><img src="https://willnorris.com/api/imageproxy/100x150/https://willnorris.com/2013/12/small-things.jpg" alt="100x150"></a>
100     | 100px square, cropping as needed         | <a href="https://willnorris.com/api/imageproxy/100/https://willnorris.com/2013/12/small-things.jpg"><img src="https://willnorris.com/api/imageproxy/100/https://willnorris.com/2013/12/small-things.jpg" alt="100"></a>
150,fit | scale to fit 150px square, no cropping   | <a href="https://willnorris.com/api/imageproxy/150,fit/https://willnorris.com/2013/12/small-things.jpg"><img src="https://willnorris.com/api/imageproxy/150,fit/https://willnorris.com/2013/12/small-things.jpg" alt="150,fit"></a>
100,r90 | 100px square, rotated 90 degrees         | <a href="https://willnorris.com/api/imageproxy/100,r90/https://willnorris.com/2013/12/small-things.jpg"><img src="https://willnorris.com/api/imageproxy/100,r90/https://willnorris.com/2013/12/small-things.jpg" alt="100,r90"></a>
100,fv,fh | 100px square, flipped horizontal and vertical | <a href="https://willnorris.com/api/imageproxy/100,fv,fh/https://willnorris.com/2013/12/small-things.jpg"><img src="https://willnorris.com/api/imageproxy/100,fv,fh/https://willnorris.com/2013/12/small-things.jpg" alt="100,fv,fh"></a>
200x,q60 | 200px wide, proportional height, 60% quality | <a href="https://willnorris.com/api/imageproxy/200x,q60/https://willnorris.com/2013/12/small-things.jpg"><img src="https://willnorris.com/api/imageproxy/200x,q60/https://willnorris.com/2013/12/small-things.jpg" alt="200x,q60"></a>
200x,png | 200px wide, converted to PNG format | <a href="https://willnorris.com/api/imageproxy/200x,png/https://willnorris.com/2013/12/small-things.jpg"><img src="https://willnorris.com/api/imageproxy/200x,png/https://willnorris.com/2013/12/small-things.jpg" alt="200x,png"></a>
cx175,cw400,ch300,100x | crop to 400x300px starting at (175,0), scale to 100px wide | <a href="https://willnorris.com/api/imageproxy/cx175,cw400,ch300,100x/https://willnorris.com/2013/12/small-things.jpg"><img src="https://willnorris.com/api/imageproxy/cx175,cw400,ch300,100x/https://willnorris.com/2013/12/small-things.jpg" alt="cx175,cw400,ch300,100x"></a>

Transformation also works on animated gifs.  Here is [this source
image][material-animation] resized to 200px square and rotated 270 degrees:

[material-animation]: https://willnorris.com/2015/05/material-animations.gif

<a href="https://willnorris.com/api/imageproxy/200,r270/https://willnorris.com/2015/05/material-animations.gif"><img src="https://willnorris.com/api/imageproxy/200,r270/https://willnorris.com/2015/05/material-animations.gif" alt="200,r270"></a>

## Getting Started

Install the package using:

    go get github.com/wojtekzw/imageproxy/cmd/imageproxy

Once installed, ensure `$GOPATH/bin` is in your `$PATH`, then run the proxy
using:

    imageproxy

This will start the proxy on port 8080, without any caching and with no host
whitelist (meaning any remote URL can be proxied).  Test this by navigating to
<http://localhost:8080/500/https://octodex.github.com/images/codercat.jpg> and
you should see a 500px square coder octocat.

### Cache

By default, the imageproxy command does not cache responses, but caching can be
enabled using the `-cache` flag.  It supports the following values:

- `memory` - uses an in-memory cache.  (This can exhaust your system's
  available memory and is not recommended for production systems)
- directory on local disk (e.g. `/tmp/imageproxy`) - will cache images
  on disk
- s3 URL (e.g. `s3://region/bucket-name/optional-path-prefix`) - will cache
  images on Amazon S3.  This requires either an IAM role and instance profile
  with access to your your bucket or `AWS_ACCESS_KEY_ID` and `AWS_SECRET_KEY`
  environmental variables be set. (Additional methods of loading credentials
  are documented in the [aws-sdk-go session
  package](https://docs.aws.amazon.com/sdk-for-go/api/aws/session/)).
- gcs URL (e.g. `gcs://bucket-name/optional-path-prefix`) - will cache images
  on Google Cloud Storage.  This requires `GCP_PRIVATE_KEY` environmental
  variable be set.
- azure URL (e.g. `azure://container-name/`) - will cache images on
  Azure Storage.  This requires `AZURESTORAGE_ACCOUNT_NAME` and
- redis URL (e.g. `redis://hostname/`) - will cache images on
  the specified redis host. The full URL syntax is defined by the [redis URI
  registration](https://www.iana.org/assignments/uri-schemes/prov/redis).
  Rather than specify password in the URI, use the `REDIS_PASSWORD`
  environment variable.

For example, to cache files on disk in the `/tmp/imageproxy` directory:

    imageproxy -cache /tmp/imageproxy

Reload the [codercat URL][], and then inspect the contents of
`/tmp/imageproxy`.  Within the subdirectories, there should be two files, one
for the original full-size codercat image, and one for the resized 500px
version.

[codercat URL]: http://localhost:8080/500/https://octodex.github.com/images/codercat.jpg

### Referrer Whitelist

You can limit images to only be accessible for certain hosts in the HTTP
referrer header, which can help prevent others from hotlinking to images. It can
be enabled by running:

    imageproxy  -referrers example.com

Reload the [codercat URL][], and you should now get an error message.  You can
specify multiple hosts as a comma separated list, or prefix a host value with
`*.` to allow all sub-domains as well.

### Host whitelist

You can limit the remote hosts that the proxy will fetch images from using the
`whitelist` flag.  This is useful, for example, for locking the proxy down to
your own hosts to prevent others from abusing it.  Of course if you want to
support fetching from any host, leave off the whitelist flag.  If origin hostname
if a CNAME to a canonical hostname, the canonical name can be used as a parameter
to `whitelist`.
This parameter allows to control access including port number eg.
`-whitelist localhost:2015` will allow access to http://localhost:2015/i.jpg
but will not allow access to http://localhost/i.jpg. And the opposite
`-whitelist localhost` will allow access to http://localhost/i.jpg and not to
http://localhost:2015/i.jpg.

Try `whitelist` out by running:

    imageproxy -whitelist example.com

Reload the [codercat URL][], and you should now get an error message.  You can
specify multiple hosts as a comma separated list, or prefix a host value with
`*.` to allow all sub-domains as well.

If you have `whilelist` and `whitelistIP` flag, host wil be allowed if it is on
any of these two lists. 


### Host whitelistIP

You can limit the remote hosts that the proxy will fetch images from using the
`whitelistIP` flag.  This is useful, for example, for locking the proxy down to
your own hosts to prevent others from abusing it.  Of course if you want to
support fetching from any host, leave off the whitelistIP flag.  Try it out by
running:

    imageproxy -whitelistIP 192.168.1.100-192.168.120,192.168.10.0/24,127.0.0.1

If you have `whilelist` and `whitelistIP` flag, host wil be allowed if it is on
any of these two lists.

`whitelistIP` will not allow you to specify port number.

### Signed Requests

Instead of a host whitelist, you can require that requests be signed.  This is
useful in preventing abuse when you don't have just a static list of hosts you
want to allow.  Signatures are generated using HMAC-SHA256 against the remote
URL, and url-safe base64 encoding the result:

    base64urlencode(hmac.New(sha256, <key>).digest(<remote_url>))

The HMAC key is specified using the `signatureKey` flag.  If this flag
begins with an "@", the remainder of the value is interpreted as a file on disk
which contains the HMAC key.

Try it out by running:

    imageproxy -signatureKey "secret key"

Reload the [codercat URL][], and you should see an error message.  Now load a
[signed codercat URL][] and verify that it loads properly.

[signed codercat URL]: http://localhost:8080/500,sXyMwWKIC5JPCtlYOQ2f4yMBTqpjtUsfI67Sp7huXIYY=/https://octodex.github.com/images/codercat.jpg

Some simple code samples for generating signatures in various languages can be
found in [URL Signing](https://github.com/willnorris/imageproxy/wiki/URL-signing).

If both a whiltelist and signatureKey are specified, requests can match either.
In other words, requests that match one of the whitelisted hosts don't
necessarily need to be signed, though they can be.

### Default Base URL

Typically, remote images to be proxied are specified as absolute URLs.
However, if you commonly proxy images from a single source, you can provide a
base URL and then specify remote images relative to that base.  Try it out by
running:

    imageproxy -baseURL https://octodex.github.com/

Then load the codercat image, specified as a URL relative to that base:
<http://localhost:8080/500/images/codercat.jpg>.  Note that this is not an
effective method to mask the true source of the images being proxied; it is
trivial to discover the base URL being used.  Even when a base URL is
specified, you can always provide the absolute URL of the image to be proxied.

### Scaling beyond original size

By default, the imageproxy won't scale images beyond their original size.
However, you can use the `scaleUp` command-line flag to allow this to happen:

    imageproxy -scaleUp true

### WebP and TIFF support

Imageproxy can proxy remote webp images, but they will be served in either jpeg
or png format (this is because the golang webp library only supports webp
decoding) if any transformation is requested.  If no format is specified,
imageproxy will use jpeg by default.  If no transformation is requested (for
example, if you are just using imageproxy as an SSL proxy) then the original
webp image will be served as-is without any format conversion.

Because so few browsers support tiff images, they will be converted to jpeg by
default if any transformation is requested. To force encoding as tiff, pass the
"tiff" option. Like webp, tiff images will be served as-is without any format
conversion if no transformation is requested.

Run `imageproxy -help` for a complete list of flags the command accepts.  If
you want to use a different caching implementation, it's probably easiest to
just make a copy of `cmd/imageproxy/main.go` and customize it to fit your
needs... it's a very simple command.

## Changes to [original] imageproxy

### Stability & monitoring

All of these changes are to help stability of imageproxy:

- `maxScaleUp` - limit scalling up to defined number of times - default 2. Works when scaling up is enabled.
   Helps to protect server memory from being exhausted
- `responseSize` - limit maximum size in bytes of image to be fetched and scaled.
  Do not try to scale too big images. Default is 20MB
- maxPixels - limit maximum size in pixels for images to be transformed. If images if larger do not try to scale it.
  Images must be 'unpacked' to memory so it helps to protect stability. (It is no ideal - smaller images can still 'unpack' to very large).
  Hardcoded default is 40MP (40 megapixels)
- statsD - send internal server data to statsD daemon
- diskcache - limit number of created files on disk (default 20000) and reload cache after restart (hardcoded in diskcache component)
- concurrency - limit concurrency of images transformation (default 15 concurrent transformations) - to preserve CPU
- `printConfig` - command line parameter to show internal config of imageproxy

### Security

- imageproxy can use HTTP_PROXY to get external images. Proxy can be set either by setting HTTP_PROXY environment variable or
    by setting command line option `httpProxy`. Command line takes precedence over environment variable.
    Example:

```shell
imageproxy -httpProxy "http://127.0.0.1:8888"
```

- imageproxy is limited to proxing only the following content-types: image/jpg, image/jpeg, image/gif, image/png. All other types generate error.
- `whilelist` - checks if orign hostaname is a CNAME to canonical name. In this case canonical name can be used in `whitelist` flag and CNAME can be used in
  hostaname in URL to be proxied. This feature is for services that have many different (maybe dynamicaly) CNAMEs to one canonical name that can be proxied.
- `whitelistIP` - limit origin to comma separated list of allowed remote hosts IP ranges. Ranges is defined as in 192.168.1.100-192.168.120 or 192.168.10.0/24 or 127.0.0.1

### Development

- `sslSkipVerify` - command line parameter to allow self-signed or expired SSL certificates on origin servers. SHOULD NOT be used in production.

## Deploying

In most cases, you can follow the normal procedure for building a deploying any
go application.  For example, I build it directly on my production debian server
using:

- `go build gtihub.com/wojtekzw/imageproxy/cmd/imageproxy`
- copy resulting binary to `/usr/local/bin`
- copy [`etc/imageproxy.service`](etc/imageproxy.service) to
 `/lib/systemd/system` and enable using `systemctl`.

Instructions have been contributed below for running on other platforms, but I
don't have much experience with them personally.

### Heroku

It's easy to vendorize the dependencies with `Godep` and deploy to Heroku. Take
a look at [this GitHub repo](https://github.com/oreillymedia/prototype-imageproxy)

### nginx

You can use follow config to prevent URL overwritting:

```nginx
  location ~ ^/api/imageproxy/ {
    # pattern match to capture the original URL to prevent URL
    # canonicalization, which would strip double slashes
    if ($request_uri ~ "/api/imageproxy/(.+)") {
      set $path $1;
      rewrite .* /$path break;
    }
    proxy_pass http://localhost:8080;
  }
```

## License

imageproxy is copyright Google, but is not an official Google product.  It is
available under the [Apache 2.0 License](./LICENSE).
