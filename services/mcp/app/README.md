# @kb-labs/mcp-app

MCP runs as a module in each project runtime. The host starts the runtime for
the project named in the URL and forwards Streamable HTTP to the MCP module.

```text
POST http://127.0.0.1:4000/api/v1/projects/<project-id>/mcp
```

Local installations accept requests without a token and keep the host on
loopback. Secured installations require a Bearer token issued by the gateway;
MCP verifies it with the same gateway JWT configuration. There is no standalone
MCP port in the installed host topology.

The `./project-module` package entry exports the project-runtime module setup.
The standalone executable remains a development service entry point.
