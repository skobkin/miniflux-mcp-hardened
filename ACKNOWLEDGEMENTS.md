# Acknowledgements

The unread digest and bulk entry-status workflows in this repository were informed by work in [`LetTTGACO/miniflux-mcp`](https://github.com/LetTTGACO/miniflux-mcp), particularly these commits:

- [`bbe63c80f18c8d4bba76df1ebe2418231fef6daa`](https://github.com/LetTTGACO/miniflux-mcp/commit/bbe63c80f18c8d4bba76df1ebe2418231fef6daa)
- [`8c9f40a861440e9ef324c0c910c25c9ec029f588`](https://github.com/LetTTGACO/miniflux-mcp/commit/8c9f40a861440e9ef324c0c910c25c9ec029f588)
- [`599043e5387183ef39e1aa17f25dfccc6e98da78`](https://github.com/LetTTGACO/miniflux-mcp/commit/599043e5387183ef39e1aa17f25dfccc6e98da78)
- [`53b37d4bacb6d75bcaa9842450b20de83ffad9ed`](https://github.com/LetTTGACO/miniflux-mcp/commit/53b37d4bacb6d75bcaa9842450b20de83ffad9ed)
- [`c20d5afe46c8b3dc06e2be757c9020d52e2c2432`](https://github.com/LetTTGACO/miniflux-mcp/commit/c20d5afe46c8b3dc06e2be757c9020d52e2c2432)
- [`782f837ea47b2a5101df0103ab19fab285991fa3`](https://github.com/LetTTGACO/miniflux-mcp/commit/782f837ea47b2a5101df0103ab19fab285991fa3)

The implementations were adapted rather than merged wholesale. This fork keeps a smaller hardened tool surface, read-only defaults, explicit write allowlisting, safe JSON integer and collection validation, purpose-built sanitized response DTOs, stable credential-safe errors, and bounded resource use. Broader API-completeness behavior from the source fork was intentionally not imported.

All existing MIT license notices remain in effect.
