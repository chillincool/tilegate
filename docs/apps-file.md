# The apps file

The apps file lists your apps, who may see each one, and optionally how the page looks. tilegate checks it every 10 seconds and reloads it when it changes, so ConfigMap edits apply without a restart.

- [Apps](#apps)
- [Icons](#icons)
- [Skipping an app's login page (`loginURL`)](#skipping-an-apps-login-page-loginurl)
- [Validation and reloading](#validation-and-reloading)
- [Theming](#theming)
- [Favourites and app ids](#favourites-and-app-ids)

## Apps

```yaml
title: My Apps                       # optional page title (default "My Apps")

apps:
  - name: Grafana                    # required
    url: https://grafana.example.com # required, http(s) only
    icon: di:grafana                 # see "Icons"
    description: Metrics             # tooltip text, also matched by search
    category: Monitoring             # groups tiles under a heading
    roles: [admins, ops]             # user needs ANY of these; omit = every signed-in user
    claim: grafana_roles             # check this claim instead of TILEGATE_ROLES_CLAIM
    loginURL: /login/generic_oauth   # where the tile links; see below
    id: grafana                      # stable id for favourites (default: slug of name)
    newTab: true                     # open in a new tab
```

Apps without a `category` come first, then each category in the order its first app appears.

**`roles` and `claim`.** By default an app's `roles` are compared against the claim(s) in [`TILEGATE_ROLES_CLAIM`](configuration.md#how-roles-are-matched). An app with `claim:` checks only that claim instead. That's useful when each app has its own Keycloak client role, delivered as a separate claim:

```yaml
- name: Radarr
  url: https://radarr.example.com
  claim: radarr_roles
  roles: [access]
```

## Icons

| `icon:` value | Result |
|---|---|
| `di:grafana` | [dashboard-icons](https://github.com/homarr-labs/dashboard-icons), loaded from jsDelivr. SVG by default; `di:name.png` for PNG. Browse names at [dashboardicons.com](https://dashboardicons.com). |
| `https://…/logo.png` | Any image URL |
| `grafana.svg` | A file in `TILEGATE_ICONS_DIR`, served by tilegate at `/icons/` |
| *(empty)* | A coloured tile with the app's first letter |

If an icon fails to load, the tile falls back to the first letter. Black logos get a faint outline in dark mode so they stay visible.

## Skipping an app's login page (`loginURL`)

Some apps show their own login page with a "Sign in with SSO" button instead of sending you to your identity provider automatically. `loginURL` makes the tile link straight to the app's SSO start, so that page is skipped. If you're already signed in to your identity provider, you land in the app with no login screen at all.

A value starting with `/` is resolved against `url`. Local login still works on the app itself, so you're not locked out if SSO breaks.

| App | `loginURL` |
|---|---|
| Grafana (generic OAuth) | `/login/generic_oauth` |
| Immich | `/auth/login?autoLaunch=1` |

Check your app version's docs; these paths change occasionally.

**Limitation:** some apps only start SSO from a form submission (a POST), which a link can't send. For those, `loginURL` gets you one page closer but not all the way. If the app has an "auto-login" or "auto-redirect" setting, that's the better fix, because it also covers bookmarks.

## Validation and reloading

The file is checked strictly, and **unknown keys are rejected**, so a typo like `role:` can't quietly make an app visible to everyone. Check a file before you deploy it:

```sh
docker run --rm -v "$PWD/apps.yaml:/a.yaml" ghcr.io/chillincool/tilegate check /a.yaml
```

If an edit to the running file is invalid, tilegate logs the error once and keeps using the previous version.

For autocompletion in editors that support YAML language server comments, start the file with:

```yaml
# yaml-language-server: $schema=https://raw.githubusercontent.com/chillincool/tilegate/main/schema/apps.schema.json
```

## Theming

Users switch between Auto, Light and Dark with the button in the header; no configuration needed. Everything below is optional.

```yaml
theme:
  mode: auto                     # default for users who haven't picked: auto | light | dark
  tileSize: 100                  # minimum tile size in px, 56–240
  accent: "#4f6bed"              # links, avatar, focus rings
  background: "#f4f5f8"          # used in both modes unless backgroundDark is set
  backgroundDark: "#0b1220"
  backgroundImage: day.jpg       # used in both modes unless backgroundImageDark is set
  backgroundImageDark: night.jpg
  logo: logo.svg                 # shown before the title
  favicon: favicon.png
  customCSS: |
    .tile { border-radius: 8px; }
```

- `logo`, `favicon` and the background images take the same forms as icons.
- **App names** wrap between words onto at most two lines; longer names end in "…", and the full name shows on hover. Tiles are never narrower than the longest single word in any name, so all tiles grow together to fit it.
- **`customCSS`** can override anything. Useful variables: `--bg`, `--surface`, `--text`, `--muted`, `--accent`, `--border` and `--tile`. To hide the theme button: `.theme-toggle { display: none; }`.
- **External stylesheets and fonts are blocked.** The page's Content-Security-Policy doesn't allow them, so `@import` from a CDN won't load. Colours are validated so they can't break out of the generated stylesheet.

## Favourites and app ids

With a [database configured](configuration.md#databases), each tile gets a star. Starred apps appear in a **Favourites** row at the top and stay in their usual section too.

- Favourites are saved per user, keyed by the token's `sub`, and follow them across devices.
- Only apps the user can currently see are shown. If someone loses a role, the matching favourites disappear; they come back if the role is restored.
- Favourites are saved by app **`id`**, which defaults to a slug of the name ("Home Assistant" → `home-assistant`). **If you rename an app, set `id` to the old slug** to keep everyone's favourites.
