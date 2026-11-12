# @jacobalberty/beenfar-api

TypeScript messages and service descriptors for the
[beenfar](https://github.com/jacobalberty/beenfar) API, generated from
`api/proto` with [protoc-gen-es](https://github.com/bufbuild/protobuf-es).
Use them with [Connect](https://connectrpc.com/docs/web/getting-started):

```ts
import { createClient } from "@connectrpc/connect";
import { createConnectTransport } from "@connectrpc/connect-web";
import { WifiNetworkService } from "@jacobalberty/beenfar-api/beenfar/config/v1/wifi_network_pb";

const transport = createConnectTransport({ baseUrl: "/" });
const wifi = createClient(WifiNetworkService, transport);
const { wifiNetworks } = await wifi.listWifiNetworks({});
```

Every module is `beenfar/<package>/v1/<file>_pb`, e.g.
`beenfar/admin/v1/auth_pb`. `@bufbuild/protobuf` is a peer dependency: it must
be the same copy your application uses.

The package version is the version of the beenfar `api` Go module (tag
`api/vX.Y.Z`) it was generated from.

## Installing

The package is published to the GitHub Packages npm registry, which needs a
token with `read:packages` even for public packages. In `.npmrc`:

```
@jacobalberty:registry=https://npm.pkg.github.com
//npm.pkg.github.com/:_authToken=${NODE_AUTH_TOKEN}
```

The code is GPL-3.0-only, like beenfar.
