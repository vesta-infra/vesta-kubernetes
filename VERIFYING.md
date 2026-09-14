Verifying a build against a cluster

Everything in here is a command for you to run. The checks are ordered by how bad it is to
get them wrong, not by how the features are organised.

Each one says what to expect and what it means if you see something else. Where a check has
a "stop" condition, stop — the later checks assume the earlier ones passed.

    export NS=vesta-system
    export PROJ=<your project>     # e.g. acme
    export ENV=<your environment>  # e.g. production
    export APP=<your app>


## 0. Before anything

    make test && make lint && make helm-lint
    make check-crds && make helm-upgrade-safety && make check-upgrader-rbac

All six pass locally today. CI runs them on every push now, so a red CI means a real
regression rather than a flaky environment.

Install or upgrade:

    helm upgrade --install vesta deploy/helm/vesta -n $NS --create-namespace

Then confirm the CRDs actually carry the new fields. This matters more than it looks:
Kubernetes prunes properties a schema does not declare, silently, on write — so a missing
field is not an error you will see, it is a setting that does not stick.

    kubectl get crd vestaconfigs.kubernetes.getvesta.sh -o yaml | grep -c 'securityProfile\|networkIsolation\|nodeMonthlyCost'
    kubectl get crd vestaapps.kubernetes.getvesta.sh -o yaml | grep -c 'desiredState\|securityProfile'

Stop if either is 0. Run `make generate && make sync-crds` and reinstall.


## 1. Security profiles — the one that can break everything at once

The default profile is `legacy` and it must set nothing at all. If it does not, every app
whose image runs as root or writes to its own filesystem breaks at the next reconcile,
across the whole instance, with no deploy to correlate it against.

Check the default is genuinely inert, before touching any setting:

    kubectl get deploy -n $PROJ-$ENV $APP -o jsonpath='{.spec.template.spec.securityContext}'; echo
    kubectl get deploy -n $PROJ-$ENV $APP -o jsonpath='{.spec.template.spec.containers[0].securityContext}'; echo

Both must print nothing. **Stop if either prints a security context** — the default is not
inert and hardening is being applied to apps that never asked for it.

Now turn on `baseline` instance-wide. This is the profile that is meant to be safe to switch
on for everything:

    kubectl patch vestaconfig vesta --type merge -p '{"spec":{"security":{"profile":"baseline"}}}'

Wait for a reconcile, then restart one app so the new pod template is used:

    kubectl rollout restart deploy/$APP -n $PROJ-$ENV
    kubectl rollout status deploy/$APP -n $PROJ-$ENV --timeout=120s

The rollout must complete. Then confirm what it applied:

    kubectl get deploy -n $PROJ-$ENV $APP -o jsonpath='{.spec.template.spec.containers[0].securityContext}' | python3 -m json.tool

Expect `allowPrivilegeEscalation: false`, `privileged: false`, and `capabilities.drop: [ALL]`.

Expect **no** `runAsNonRoot` and **no** `readOnlyRootFilesystem`. Those two are what break
real images, and they belong to `restricted`. If baseline sets them, the ladder has
collapsed into a single switch and baseline is no longer safe to turn on broadly.

If your app listens on a port below 1024, also check the capability was handed back:

    kubectl get deploy -n $PROJ-$ENV $APP -o jsonpath='{.spec.template.spec.containers[0].securityContext.capabilities}'; echo

Expect `NET_BIND_SERVICE` in `add`. Without it the container cannot bind its own port and
crashes — the image is fine, the config looks right, and the pod simply fails.

Now `restricted`, on **one app only**. Do not set this instance-wide on a cluster you care
about until you know which images survive it:

    kubectl patch vestaapp $APP -n $NS --type merge -p '{"spec":{"securityProfile":"restricted"}}'
    kubectl rollout status deploy/$APP -n $PROJ-$ENV --timeout=120s

If it does not roll out, that is the expected failure for an image not built for it — check
`kubectl describe pod` for `CreateContainerConfigError` (the image wants to run as root) or
read-only filesystem errors in the logs. Both are the setting working, not Vesta breaking.

Confirm it can write where it needs to:

    kubectl exec -n $PROJ-$ENV deploy/$APP -- sh -c 'touch /tmp/probe && echo tmp-writable'
    kubectl exec -n $PROJ-$ENV deploy/$APP -- id -u

Expect `tmp-writable` and a non-zero uid. A read-only root filesystem with no writable /tmp
is not a hardening setting, it is an outage.

Put it back when you are done:

    kubectl patch vestaconfig vesta --type merge -p '{"spec":{"security":{"profile":"legacy"}}}'
    kubectl patch vestaapp $APP -n $NS --type merge -p '{"spec":{"securityProfile":null}}'


## 2. Network isolation — check the verdict, not the objects

The trap here is that NetworkPolicy is enforced by your CNI, not by Kubernetes. A cluster
running flannel accepts every policy, lists them back, and filters nothing. The objects
existing proves nothing at all.

Find out what Vesta concluded about your cluster **before** relying on it:

    kubectl patch vestaconfig vesta --type merge -p '{"spec":{"security":{"networkIsolation":{"enabled":true}}}}'
    sleep 30
    kubectl get vestaenvironment $ENV -n $NS -o jsonpath='{.status.networkIsolation}' | python3 -m json.tool

Read `enforced` and `enforcementKnown` together:

- `enforced: true` — your CNI implements it. The policies are doing something.
- `enforced: false, enforcementKnown: true` — flannel or similar. **The policies are
  decorative.** Nothing is isolated. This is the case worth knowing about.
- `enforcementKnown: false` — Vesta did not recognise your CNI. Read `note` and check your
  plugin's docs yourself; treat isolation as unverified until you do.

Then confirm apps are still reachable, because a default-deny that also blocks the ingress
controller takes every app in the environment offline:

    kubectl get networkpolicy -n $PROJ-$ENV
    curl -sS -o /dev/null -w '%{http_code}\n' https://<your app's domain>/

Expect the same status you got before enabling it. **Stop and disable isolation if the app
went unreachable** — the trusted-namespace list does not match where your ingress controller
actually runs:

    kubectl get pods -A -l app.kubernetes.io/name=traefik -o jsonpath='{.items[0].metadata.namespace}'; echo

Set that namespace explicitly:

    kubectl patch vestaconfig vesta --type merge \
      -p '{"spec":{"security":{"networkIsolation":{"enabled":true,"trustedNamespaces":["<ns>","kube-system"]}}}}'

Note that setting the list **replaces** the defaults rather than adding to them, so include
everything you need — including wherever Prometheus runs, if you scrape apps.

Two things that should still work with isolation on, because both would be easy to break:

    # an app reaching its own database in the same namespace
    kubectl exec -n $PROJ-$ENV deploy/$APP -- sh -c 'nc -z -w3 <addon-service> 5432 && echo same-ns-ok'
    # DNS, which an egress deny would kill (there should be no egress policy at all)
    kubectl get networkpolicy -n $PROJ-$ENV -o jsonpath='{.items[*].spec.policyTypes}'; echo

The second must print only `Ingress`, never `Egress`.

Turning it off must remove the policies, not leave a deny behind:

    kubectl patch vestaconfig vesta --type merge -p '{"spec":{"security":{"networkIsolation":{"enabled":false}}}}'
    sleep 30
    kubectl get networkpolicy -n $PROJ-$ENV

Expect none. **A leftover default-deny here takes the environment offline with no object
left to explain why** — that is the worst outcome available in this section.


## 3. Quotas — the LimitRange is what makes them usable

A ResourceQuota naming `requests.cpu` makes Kubernetes *require* a request on every new pod
in that namespace, and refuse the ones without. Vesta ships a LimitRange alongside for
exactly this reason. Check both exist together:

    kubectl get resourcequota,limitrange -n $PROJ-$ENV

Set a quota through the API (or the Quotas card on the project page) and read back what the
operator concluded before enforcing anything:

    kubectl get vestaenvironment $ENV -n $NS -o jsonpath='{.status.quota}' | python3 -m json.tool

`committed` is what the environment's apps add up to at their **autoscaling ceiling**, not
their current replica count. A quota that fits today's replicas and not tomorrow's does not
fail when you set it — it fails when the HPA tries to scale, as a `FailedCreate` on a
ReplicaSet nobody is watching.

If `wouldExceed` is true, the UI should be showing you the warning banner. Confirm it does:
that banner read the wrong field name until this session, so it is worth one look.

Then prove the quota actually refuses something, with the LimitRange in place:

    kubectl run quota-probe --image=nginx -n $PROJ-$ENV --restart=Never
    kubectl get pod quota-probe -n $PROJ-$ENV
    kubectl delete pod quota-probe -n $PROJ-$ENV --ignore-not-found

With a LimitRange present this pod is admitted with defaulted requests. Without one it is
rejected outright — if you see `must specify requests.cpu`, the LimitRange is missing and
any pod created outside Vesta in that namespace will fail.


## 4. Scale to zero

    kubectl patch vestaapp $APP -n $NS --type merge \
      -p '{"spec":{"sleep":{"enabled":true,"autoSleep":true,"inactivityTimeout":"5m","minAwake":"2m"}}}'

Sleep manually first, which tests the path without waiting on Prometheus:

    kubectl patch vestaapp $APP -n $NS --type merge -p '{"spec":{"desiredState":"sleeping"}}'
    kubectl get deploy -n $PROJ-$ENV $APP -o jsonpath='{.spec.replicas}'; echo

Expect `0`. This is the path that silently did nothing for several releases — the instruction
used to be written to `status.phase`, which a status subresource discards — so a non-zero
here means the regression is back.

Now wake it with traffic:

    time curl -sS -o /dev/null -w '%{http_code}\n' https://<your app's domain>/

Expect a success status after a pause of a few seconds, and:

    kubectl get deploy -n $PROJ-$ENV $APP -o jsonpath='{.spec.replicas}'; echo

Expect non-zero. Then the check you asked for specifically — **a health check must not wake a
sleeping app**, or an uptime monitor keeps it running forever and scale-to-zero never saves
anything:

    kubectl patch vestaapp $APP -n $NS --type merge -p '{"spec":{"desiredState":"sleeping"}}'
    sleep 10
    curl -sS -o /dev/null -w '%{http_code}\n' https://<your app's domain>/healthz
    kubectl get deploy -n $PROJ-$ENV $APP -o jsonpath='{.spec.replicas}'; echo

Replicas must still be `0`, **with no configuration** — the well-known health paths
(`/healthz`, `/health`, `/readyz`, `/livez`, `/healthcheck`, `/-/healthy`, `/-/ready`) are
excluded by default. They have to be: an uptime check polling every thirty seconds wakes the
app every thirty seconds, so it never stays down and the feature saves nothing.

`/status` and `/ping` are deliberately **not** in that list — both are real application
endpoints often enough that answering them from the activator would be worse than the problem
being solved. Add them per app if your monitor uses one:

    kubectl patch vestaapp $APP -n $NS --type merge \
      -p '{"spec":{"sleep":{"noWakePaths":["/healthz","/ping"]}}}'

Setting the list **replaces** the defaults rather than adding to them.

Then confirm a normal path still wakes it, so the no-wake list has not simply swallowed
everything:

    curl -sS -o /dev/null https://<your app's domain>/
    kubectl get deploy -n $PROJ-$ENV $APP -o jsonpath='{.spec.replicas}'; echo

One case no path list can help with: if your monitor polls `/` itself, it will wake the app
every time. Point it at `/healthz` instead.

And check the app is told why it is or is not sleeping:

    kubectl get vestaapp $APP -n $NS -o jsonpath='{.status.sleepReason}'; echo

Empty is a problem: without it, somebody who turned on auto-sleep and sees the app still
running cannot tell whether it is busy, whether Prometheus is missing, or whether Vesta is
simply not looking.

    kubectl patch vestaapp $APP -n $NS --type merge -p '{"spec":{"desiredState":"running"}}'


## 5. Cost

The sampler writes every five minutes and works with no Prometheus at all — replica count
and container requests fully answer "what is this reserving", which is the whole basis of
the figure. metrics-server only adds the usage column.

    kubectl logs -n $NS deploy/vesta-api | grep '\[cost\]'

Then wait two sampling intervals and check the API:

    curl -sS -H "Authorization: Bearer $TOKEN" \
      "https://<your vesta>/api/v1/projects/$PROJ/costs?window=24h" | python3 -m json.tool

`estimated: true` means you are on the built-in default rate card, derived from one commodity
node. Price your own cluster instead:

    kubectl patch vestaconfig vesta --type merge \
      -p '{"spec":{"cost":{"nodeMonthlyCost":200,"nodeVCPUs":8,"nodeMemoryGiB":32,"currency":"USD"}}}'

A sleeping app must cost nothing for the intervals it was asleep — the sampler records
`status.replicas`, not the desired count. Storage still costs, because a volume that exists
is a volume you are paying for.


## 6. Git and registry

    curl -sS -H "Authorization: Bearer $TOKEN" https://<your vesta>/api/v1/git/connections | python3 -m json.tool

Per provider, push a commit and confirm two things: a build triggers, and the commit status
moves from pending to success or failure. Every Vesta build used to leave a permanently
pending check — `watchBuild` never told the notifier anything — so a check stuck yellow means
that regression is back.

An unsigned webhook must be rejected once `webhooks.allow_unsigned` is off:

    curl -sS -o /dev/null -w '%{http_code}\n' -X POST https://<your vesta>/api/v1/webhooks/github \
      -H 'Content-Type: application/json' -d '{"ref":"refs/heads/main"}'

Expect 401 or 403. **A 200 here is an unauthenticated deploy trigger.**

For Harbor, add the credential as `https://harbor.example.com:443` and confirm Vesta warns
that the normalized auths key will not match your images' host — then apply the fix and
confirm the image pulls. That mismatch fails only as `ImagePullBackOff`, with nothing
anywhere saying why.


## 7. Secret scope

Every registry credential that existed before this field did carries no scope, and an absent
scope means global. That is deliberate — anything else would have cut apps off from the
credentials they already pull with — so check it held:

    kubectl get vestasecrets -n $NS -l kubernetes.getvesta.sh/type=registry \
      -o custom-columns=NAME:.metadata.name,SCOPE:.spec.scope,PROJECT:.spec.project

Existing rows show an empty SCOPE. They are global, and every credential that worked
yesterday still works.

Set the instance default so new credentials are private by default:

    kubectl patch vestaconfig vesta --type merge \
      -p '{"spec":{"security":{"defaultSecretScope":"project"}}}'

This must **not** reclassify anything. Re-run the command above and confirm the existing rows
still show an empty SCOPE. If they changed, a migration ran that should not have, and
credentials an app depends on may now be out of its reach.

Then create one through the UI, scoped to a project, and check it from an account that is not
a member of that project:

    curl -sS -H "Authorization: Bearer $OTHER_TOKEN" \
      https://<your vesta>/api/v1/secrets/registry | python3 -m json.tool

The project-scoped credential must be absent from the list entirely — not present-and-
redacted. And a direct hit on it must 404 rather than 403, because a caller who may not use a
credential should not learn it exists:

    curl -sS -o /dev/null -w '%{http_code}\n' -H "Authorization: Bearer $OTHER_TOKEN" \
      "https://<your vesta>/api/v1/secrets/registry/<name>/repositories"

Expect 404. **A 200 here means a credential is usable by someone outside its project.**


## 8. Registry passwords are out of the CRD

The operator migrates a plaintext password into a Kubernetes Secret and then clears it. The
order matters more than anything else here: a registry password exists nowhere else, so
clearing one that was not durably copied first loses it, and the only symptom is an
ImagePullBackOff nobody can fix without knowing the original.

Before upgrading, note what you have:

    kubectl get vestasecrets -n $NS -l kubernetes.getvesta.sh/type=registry \
      -o custom-columns=NAME:.metadata.name,HAS_PLAINTEXT:.spec.dockerConfig.password

After the operator has run a pass:

    kubectl get vestasecrets -n $NS -l kubernetes.getvesta.sh/type=registry \
      -o custom-columns=NAME:.metadata.name,PLAINTEXT:.spec.dockerConfig.password,REF:.spec.dockerConfig.passwordSecretRef.name
    kubectl get secrets -n $NS -l kubernetes.getvesta.sh/purpose=registry-password

Every credential should show an empty PLAINTEXT and a REF, with a matching Secret. A row with
**both empty** is the bad case — check the operator log before doing anything else:

    kubectl logs -n $NS deploy/vesta-operator | grep -i "registry password"

The migration refuses to clear the original unless it has read the Secret back and confirmed
the value, so a failure should leave the plaintext in place and retry. Confirm the credential
still works either way:

    kubectl rollout restart deploy/$APP -n $PROJ-$ENV
    kubectl rollout status deploy/$APP -n $PROJ-$ENV --timeout=120s

An `ImagePullBackOff` here means the password did not survive. And confirm the derived pull
secret was rebuilt from the Secret rather than from an empty field:

    kubectl get secret <credential-name> -n $NS -o jsonpath='{.data.\.dockerconfigjson}' \
      | base64 -d | python3 -m json.tool

The `auth` field must decode to `username:password`, not `username:`.

Creating a global credential is now admin-only. From a non-admin account:

    curl -sS -o /dev/null -w '%{http_code}\n' -X POST -H "Authorization: Bearer $DEV_TOKEN" \
      -H 'Content-Type: application/json' \
      -d '{"name":"probe","registry":"docker.io","username":"u","password":"p","scope":"global"}' \
      https://<your vesta>/api/v1/secrets/registry

Expect 403.


## What is not covered here

Project **export bundles** still carry registry passwords in plain text. The bundle resolves
the password out of the Secret so transfers keep working, which means an exported bundle is a
file containing live credentials. Treat one as a secret in its own right. Changing that would
mean transfers producing credentials that need re-entering by hand, which is a product
decision rather than a bug fix.
