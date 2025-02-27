# Tsnet Gateway

This is a **Tailscale-powered gateway** which runs in userspace, that can:
- Act as a **reverse proxy** to route requests inside your Tailnet.
- Act as an **outgoing proxy** to forward external requests.
- Run in **both modes** as a gateway.

---

## Flags

| Flag            | Description |
|----------------|-------------|
| `--authkey`    | **(Required)** Tailscale authentication key. |
| `--type`       | Proxy mode: `rproxy` (reverse proxy), `proxy` (outgoing proxy), or `gateway` (both). |
| `--routes`     | Comma-separated route mappings (e.g. `"/api/=http://localhost:9696,/app/=http://localhost:8081"`). Only used in `rproxy` or `gateway` mode. |
| `--routes-file` | Path to a JSON file defining route mappings. **Note:** If both `--routes` and `--routes-file` are provided, `--routes` takes precedence, and the routes from `--routes-file` will be ignored.|
| `--proxy-port`  | Port for forward proxy. Default: `8080`. |
| `--base`       | Base directory for Tailscale data. Defaults to current directory. |
| `--rproxy-port`| Port for reverse proxy. Default `8443`|
| `--hostname`| Hostname in the Tailnet. Default `tsnet-gateway`|
| `--admin-port`| Port for admin server. Currently only to change hostname|

---

## Usage
```sh
go build -o tsnet-gateway main.go
./tsnet-gateway --authkey="tskey-auth-XXXX" --routes="/api/=http://localhost:9696"
```

## Why I Built This

I needed Tailscale for my app but didn’t want users to install it separately, and I wanted it to run in userspace. I embedded **Tsnet Gateway** as a sidecar to make my app accessible on the Tailnet immediately upon install.

