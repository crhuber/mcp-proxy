# mcp-proxy

Add MCP support to any REST API - no code changes required.

We've spent decades building REST APIs. Adding MCP support shouldn't mean
reimplementing your business logic. `mcp-proxy` exposes your existing REST
endpoints as MCP tools automatically, with zero changes to your API.

## How It Works

`mcp-proxy` loads a YAML config describing one or more upstream REST APIs, dynamically registers MCP tools for them, and translates each
tool call into an HTTP request against the real upstream API and back.

## Quick Start

Say for example, you already have an REST API server at https://api.gold-api.com but you want to be able to query it via MCP.

To achieve this, create a `config.yaml`

```yaml
endpoints:
  - name: gold-api
    upstream:
      base_url: "https://api.gold-api.com"
      timeout: 10s
      auth:
        type: "none"
    tools:
      - name: getPrice
        description: "Fetch gold price"
        http:
          method: GET
          path: "/price/XAU"
```

Run the server.

```bash
mcp-proxy --config config.yaml
```

What happens here is a new MCP tool called `getPrice` is registered on the server. When this MCP tool is called the server makes a `HTTP GET` request to `https://api.gold-api.com/price/XAU` and returns the result to the MCP Client.


Verify its working by making MCP tool call using `curl`

```bash
curl -X POST http://localhost:8080/mcp \
        -H "Content-Type: application/json" \
        -H "Accept: application/json, text/event-stream" \
        -d '{
      "jsonrpc": "2.0",
      "id": 1,
      "method": "tools/call",
      "params": {
        "name": "gold-api_getPrice",
        "arguments": {}
      }
    }'
```

Response:

```bash
event: message
data: {"jsonrpc":"2.0","id":1,"result":{"content":[{"type":"text","text":"{\"currency\":\"USD\",\"price\":4409.299805}"}],"structuredContent":{"currency":"USD","price":4409.299805}}}
```

Congrats! We have now adapted our API to be MCP ready with no code changes to the API!


## Install

```bash
go install github.com/crhuber/mcp-proxy/cmd/mcp-proxy@latest
```

Or build from source:

```bash
go build -o mcp-proxy ./cmd/mcp-proxy
```

Or Use [kelp](https://github.com/crhuber/kelp)

```bash
kelp add crhuber/mcp-proxy --install
```

## Usage

```bash
mcp-proxy --config config.yaml
```

### Flags

| Flag | Env var | Default | Description |
| --- | --- | --- | --- |
| `--config` | `MCP_PROXY_CONFIG` | *(required)* | Path to the proxy config YAML file |
| `--listen` | `MCP_PROXY_LISTEN_ADDR` | `:8080` | Address to listen on |
| `--auth-mode` | `MCP_PROXY_AUTH_MODE` | `none` | `none` \| `bearer` — whether the proxy's own `/mcp` endpoint requires a bearer token |
| `--log-level` | `MCP_PROXY_LOG_LEVEL` | `info` | `debug` \| `info` \| `warn` \| `error` |
| `--shutdown-grace` | `MCP_PROXY_SHUTDOWN_GRACE` | `15s` | How long to wait for in-flight requests to finish on shutdown |

When `--auth-mode=bearer`, set `MCP_PROXY_BEARER_TOKEN` in the environment
(it is intentionally not a flag so it never shows up in `--help` output or
process argv).

## Configuration

The config file describes one or more upstream endpoints and the tools that
should be generated for each. See [`config.yaml.example`](docs/config.yaml.example)
for a full annotated example, including path/query parameters, request body
templating, and response field selection.


### Upstreams

To connect to an upstream REST API, you need to define your upstream `base_url`, `timeout`, and `auth` type.

```yaml
endpoints:
  - name: gold-api
    upstream:
      base_url: "https://api.gold-api.com"
      timeout: 10s
      auth:
        type: "none"
```

The base_url should be defined *without* a trailing slash `/`.

Auth types supported are:

`bearer`
```yaml
      auth:
        type: bearer                      # bearer | header | query | none
        env: ORDERS_API_KEY               # secret ALWAYS comes from this env var, never literal in file
```

`header`
```yaml
      auth:
        type: header
        header: X-API-Key
        type: "none"
        env: ORDERS_API_KEY               # secret ALWAYS comes from this env var, never literal in file
```

`none`
```yaml
      auth:
        type: "none"
```

#### Environment variables

For any key defined in `env` it must be set in environment variables before starting the server. In the preceeding examples `ORDERS_API_KEY` must be set


### Tools

Tools are the MCP tools to create on the MCP server. Each tool has `name`, `description`, and `parameters`

```yaml
    tools:
      - name: createInvoice
        description: "Create a invoice for a customer."
        parameters:
          type: object
          required: [customerId]
          properties:
            customerId:
              type: string
              description: "Customer to bill."
```


### HTTP

Whenever a toolcall is made to the attached tool, a coresponding http request is made to the upstream.

The `http` object controls what how the HTTP request upstream is made. The usual parameters for a HTTP request including `method`, `path` and `body`


```yaml
    tools:
      - name: getPrice
        description: "Fetch gold price"
        http:
          method: GET
          path: "/price/XAU"
```

For `POST` and `PUT` operations, a JSON request body can also be sent by adding the `body`

```yaml
        http:
          method: POST
          path: "/v1/invoices"
          body: # literal JSON template of exactly what to POST
            customerId: "123"

```

### Parameter Variables

Suppose your tool has properties `customerId` and `currency` but you want to reference those when you make a http POST method upstream. You can reference them using `{parameter}` syntax.

```yaml
    tools:
      - name: createInvoice
        description: "Create a new draft invoice for a customer."
        parameters:
          type: object
          required: [customerId]
          properties:
            customerId:
              type: string
              description: "Customer to bill."
            currency:
              type: string
              description: "ISO-4217 currency code."
              default: "USD"
        http:
          method: POST
          path: "/v1/invoices"
          body:                            # literal JSON template of exactly what to POST
            customerId: "{customerId}"      # "{name}" -> substituted with that parameter's runtime value
            currency: "{currency}"
```

This also works in paths

```yaml
        http:
          method: POST
          path: "/v1/invoices/{customerId}"

```


### HTTP Response Formatting

Suppose our API call to `https://api.gold-api.com/price/XAU` returns JSON response

```json
{
    "currency": "USD",
    "currencySymbol": "$",
    "exchangeRate": 1.0,
    "name": "Gold",
    "price": 4394.299805,
    "symbol": "XAU",
    "updatedAt": "2026-08-17T12:23:02Z",
    "updatedAtReadable": "a few seconds ago"
}
```

In many MCP cases it is preferred to use structured but compact text over raw JSON dumps.
For something like this, a lightly formatted markdown table or bullet list is often easier for the model to reason over than raw JSON and cheaper in tokens.

To achieve this we can use the `http.response.select` object to `JQ` style select only the fields we need

```yaml
http:
  method: GET
  path: "/price/XAU"
  response:
    select:
      currency: "{currency}"
      price: "{price}"
```

Here we use `"{currency}"` variables to return fields from the JSON response which will return

```
currency: USD
price: 4394.299805
```
A tool call result in MCP follows this structure:

```json
{
  "content": [
    {
      "type": "text",
      "text": "
        currency: USD
        price: 4394.299805
      "
    }
  ],
  "isError": false
}
```

Other available response formatting functions:

- `{path.to.field}` — plain field extraction
- `{arrayField[].path}` — map a sub-path over every element of an array
- `{{literal}}` — escape hatch for a literal string that looks like a path (e.g. {{id}} → the string "{id}")
- `{truncate(path, maxLen)}` — truncate a string (or every string in an array-valued path) to maxLen


### Protecting MCP Proxy

`mcp-proxy` can be protected with an API key and setting `MCP_PROXY_AUTH_MODE` and `MCP_PROXY_BEARER_TOKEN`

```
export MCP_PROXY_AUTH_MODE=bearer
openssl rand -base64 32
export MCP_PROXY_BEARER_TOKEN=****
```

To connect to `mcp-proxy` you will need to pass header

```
Authorization: Bearer ***
```


## Development

```bash
go build ./...
go vet ./...
go test ./...
```
