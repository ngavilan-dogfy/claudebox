<div align="center">

# 📦 claudebox

**Claude Code en un sandbox Docker desechable. Un comando: `cbox`.**

*Internet sí. Tu máquina y tu LAN, nunca.*

![Release](https://img.shields.io/github/v/release/ngavilan-dogfy/claudebox?color=blueviolet)
![Platform](https://img.shields.io/badge/platform-macOS%20%7C%20Linux%20%7C%20WSL2-blue)
![Runtime](https://img.shields.io/badge/runtime-Docker%20%7C%20Podman%20%7C%20OrbStack-2496ED?logo=docker&logoColor=white)
![Go](https://img.shields.io/badge/Go-Bubble%20Tea%20TUI-00ADD8?logo=go&logoColor=white)
![License](https://img.shields.io/badge/license-MIT-green)
![Claude Code](https://img.shields.io/badge/Claude%20Code-sandboxed-D97757?logo=anthropic&logoColor=white)

</div>

---

## ✨ Por qué

Claude Code corriendo con tu usuario ve **todo**: tus llaves SSH, tus tokens,
tu LAN. `claudebox` lo mete en un contenedor efímero donde solo existen el
repo actual y una identidad dedicada del agente — y aún así puede navegar
internet para leer documentación. Si algo sale mal, el daño máximo es el
directorio del proyecto.

- 🗑️ **Efímero** — cada sesión es un contenedor nuevo, destruido al salir
- 🔥 **Firewall de egress** — internet abierto, pero el host y los rangos privados bloqueados por iptables
- 🔑 **Identidad propia** — llave SSH y gitconfig dedicados; tus llaves personales no existen dentro
- 🧪 **`yolo` seguro** — `--dangerously-skip-permissions` solo dentro del sandbox
- 🌳 **Entornos con nombre** — logins/configs separados con `cbox @work`, `cbox @personal`
- ⚡ **Rápido** — binario Go único con `exec` directo al runtime; arranque subsegundo con OrbStack
- 📦 **Autocontenido** — el contexto de build va embebido en el binario y la imagen se etiqueta
  por hash de contenido: una imagen obsoleta no puede reutilizarse jamás
- 🖥️ **Feedback de verdad** — panel de sesión al arrancar, build con progreso, `doctor` con
  diagnóstico completo y test del firewall en vivo

## 🚀 Instalación

```bash
curl -fsSL https://raw.githubusercontent.com/ngavilan-dogfy/claudebox/main/install.sh | bash
```

El instalador se encarga de **todo**: baja el binario de tu plataforma desde
GitHub Releases (o compila desde fuente si tienes Go), añade `cbox` al PATH
de tu shell, crea la llave SSH dedicada del agente si no existe, construye la
imagen, barre imágenes obsoletas y termina con un `cbox doctor` completo.
Re-ejecútalo para actualizar — es idempotente.

<details>
<summary>O manualmente, si prefieres leer antes de ejecutar (👏)</summary>

```bash
git clone https://github.com/ngavilan-dogfy/claudebox ~/claudebox
cd ~/claudebox && go build -o ~/.local/bin/cbox . && cbox build
```
</details>

Requisitos: un runtime de contenedores ([OrbStack](https://orbstack.dev)
recomendado en macOS, Docker o Podman en Linux, Docker Desktop en WSL2).
En Linux, `cbox` ajusta el UID del contenedor al tuyo automáticamente para
que los permisos de los bind mounts no se rompan.

Verifica todo con:

```bash
cbox doctor
```

## 🖥️ Dashboard TUI: `cbox ui`

Centro de control interactivo (Bubble Tea) con tres pestañas y filtrado
fuzzy tipo fzf en todas (`/`):

```
◆ claudebox  login: tu@cuenta.com · shared by all envs
  1 Sessions    2 Projects    3 Envs

│ cbox-sprout-game-7417
│ env default · up 2 hours · claude-box:hf1c628a

enter shell into session · x stop · r refresh · / filter · tab switch · q quit
```

- **Sessions** — gestionadas (●/○ attached/detached) y directas. `enter`
  se attachea (o abre shell en las no gestionadas), `n` nueva, `x` cierra
  con confirmación, `r` refresca.
- **Projects** — historial de proyectos recientes; `enter` arranca una
  sesión gestionada ahí (respeta el `.cbox.conf` del proyecto).
- **Envs** — entornos con su cuenta; `enter` arranca sesión en el directorio
  actual con ese entorno.

Navegación: `tab`/`shift+tab` o `1`/`2`/`3` entre pestañas, `↑↓`/`jk` en las
listas, `/` filtra, `q` sale.

## 🕹️ Uso

### Sesiones gestionadas (lo god ✨)

Sesiones con nombre que **sobreviven a cerrar la terminal**, respaldadas por
tmux — creas, navegas, te attacheas desde cualquier sitio y cierras, todo
desde la terminal:

```bash
cd ~/mi-proyecto
cbox new                 # sesión "sprout-game", se attachea ya
cbox new feature-x       # segunda sesión del mismo proyecto, en paralelo
# … Ctrl+b d para salir dejándola corriendo …
cbox ls                  # ● attached / ○ detached + estado del contenedor
cbox attach feat         # vuelve (fuzzy match; alias: cbox a feat)
cbox kill feature-x      # cierra sesión + contenedor
cbox kill --all
```

### Directo (efímero, ligado a la terminal)

```bash
cbox                        # claude interactivo, sandboxed
cbox yolo                   # sin prompts de permisos (la red sigue filtrada)
cbox -p "arregla los tests" # one-shot; los args pasan directos a claude
```

| Comando | Qué hace |
|---|---|
| `cbox [@env] [args...]` | Claude interactivo en el directorio actual |
| `cbox [@env] yolo [args...]` | `--dangerously-skip-permissions`, red aún filtrada |
| `cbox [@env] shell` | bash dentro del sandbox |
| `cbox [@env] login` | solo el flujo de login de ese entorno |
| `cbox ui` | dashboard interactivo: sesiones, proyectos, entornos |
| `cbox ps` | sesiones corriendo ahora mismo |
| `cbox envs` | entornos existentes |
| `cbox doctor` | chequeo completo (incluye test en vivo del firewall) |
| `cbox build` / `update` | (re)construir la imagen / actualizar claude (+ limpieza) |
| `cbox cleanup` | borrar imágenes obsoletas y contenedores muertos |

**Sesiones concurrentes:** sin límite. Cada invocación es un contenedor con
nombre único; abre N terminales y proyectos a la vez.

## 🌐 Red

| `CBOX_NET` | Internet | Host + LAN | Para qué |
|---|---|---|---|
| `open` *(default)* | ✅ todo | ❌ bloqueado | día a día y `yolo` |
| `allowlist` | solo `CBOX_ALLOWED_DOMAINS` | ❌ | desatendido / paranoia |
| `full` | ✅ | ✅ | necesitas un servicio local |

`open` bloquea `host.docker.internal`, 10/8, 172.16/12, 192.168/16,
link-local, CGN y el rango de OrbStack: Claude lee toda la documentación que
quiera en internet, pero no puede tocar tu máquina ni tu red.

> ⚠️ **Límite honesto:** el DNS queda abierto (hace falta para resolver), así
> que esto frena el acceso al host y la exfiltración casual — no un túnel DNS
> dedicado.

## 🔑 SSH e identidad

| `CBOX_SSH` | Cómo funciona |
|---|---|
| `key` *(default)* | Monta `~/.ssh/claude_agent` **read-only**. Créala: `ssh-keygen -t ed25519 -f ~/.ssh/claude_agent` |
| `agent` | Reenvía el socket del ssh-agent; la llave privada **nunca entra** al contenedor. Combínalo con `ssh-add -c` o [Secretive](https://github.com/maxgoedjen/secretive) para confirmar cada firma con Touch ID |
| `none` | Sin SSH |

La identidad git del agente vive en `~/.config/cbox/gitconfig` (el instalador
crea una plantilla). Consejo: en GitHub usa *deploy keys* por repo o un
*fine-grained PAT* — revocar al agente nunca debe costar rotar tu identidad.

## ⚙️ Config

Dos niveles, mismo formato `CBOX_*=valor`: **global** en
`~/.config/cbox/config` (el instalador crea la plantilla comentada — tu
lugar único para ajustarlo todo) y **por proyecto** en `.cbox.conf` en la
raíz del repo, que pisa a la global:

```bash
CBOX_PORTS="3000 5173"                # dev servers publicados en 127.0.0.1
CBOX_MOUNTS="$HOME/datasets:/data:ro" # mounts extra
CBOX_NET="allowlist"
CBOX_ALLOWED_DOMAINS="api.anthropic.com,github.com,registry.npmjs.org"
CBOX_MEMORY="12g"                     # techo, no reserva
CBOX_CPUS="4"
CBOX_SSH="agent"
CBOX_ENV="work"
```

## 🧭 Login

**Una vez, para todos los cbox — y separado de tu claude del host.** La
identidad de cbox vive en un volumen de auth compartido (`claude-box-auth`)
que todas las sesiones y entornos enlazan por symlink: te logueas una vez y
cualquier proyecto y cualquier `@env` entra directo. Un `/login` o refresh de
token dentro de una sesión se publica de vuelta al volumen compartido en el
siguiente arranque (symlink auto-reparable). Tu `claude` del host usa su
propio almacén y **nunca se mezcla** — cbox ni siquiera reenvía
`CLAUDE_CODE_OAUTH_TOKEN` del host. `cbox doctor` y `cbox envs` te dicen con
qué cuenta está logueado cbox.

El flujo la primera vez: `cbox login` — Claude muestra una **URL para abrir
en el navegador del host** y pegas el código de vuelta (el redirect
automático no funciona en contenedor; este flujo sí, siempre).

Plan B: `claude setup-token` en el host y exporta `CLAUDE_CODE_OAUTH_TOKEN`
(cbox lo pasa al contenedor automáticamente).

## 🛡️ Garantías

| | `cbox` | `cbox yolo` |
|---|---|---|
| Filesystem del host | solo `$PWD` | solo `$PWD` |
| Llaves/tokens personales | no existen dentro | no existen dentro |
| Capabilities | 5 mínimas (iptables + drop a node + chown del volumen de auth), sin sudo en la imagen | igual |
| Permisos de Claude | prompts normales | sin prompts |
| Red | internet sí, host/LAN no | igual |
| Recursos | techo 8 GB / 2000 pids / swap=RAM | igual |

## 🩹 Troubleshooting

- **"refusing to run from ... your home directory"** — `cbox` monta el
  directorio actual dentro del sandbox; lanzarlo desde `$HOME` (o un ancestro)
  le daría al agente tus llaves y tokens reales. Haz `cd` a un proyecto.
- **"still root after entrypoint — stale image"** — tu imagen es anterior a la
  migración a setpriv: `cbox update` y listo.
- **Dos sesiones del mismo proyecto y puertos** — la segunda falla por puerto
  ocupado; quita `CBOX_PORTS` en ella.
- **TUI con caracteres rotos** — la imagen fija `LANG=C.UTF-8`; haz
  `cbox update` si vienes de una versión antigua.
- **Necesito tocar un servicio del host** — `CBOX_NET=full cbox`, sabiendo lo
  que haces.
- **¿Podman?** — `CBOX_RUNTIME=podman` (env o `.cbox.conf`).
- Cualquier otra cosa: `cbox doctor` primero.

## 📄 Licencia

[MIT](LICENSE)
