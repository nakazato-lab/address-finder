## To Deploy on the cluster

**1. Install the CRDs**

```sh
make install
```

**2. Deploy the controller**

```sh
make deploy IMG=ghcr.io/tryuuu/address-finder:<tag>
```

This creates the `address-finder-system` namespace and the
`address-finder-controller-manager` Deployment.

**3. Create an `AddressSync` instance**

The sample assumes an `ndn` namespace:

```sh
kubectl create ns ndn
kubectl apply -k config/samples/
kubectl get addresssync -n ndn -o yaml   # check status.currentAddress
```

## To Uninstall

```sh
kubectl delete -k config/samples/
make uninstall
make undeploy
```

## License

Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
