# claudebox

Claude Code en un sandbox Docker desechable, con un solo comando: `cbox`.
Internet sí; tu máquina y tu LAN, nunca.

## Instalación

```bash
git clone <repo> ~/claudebox && ~/claudebox/install.sh
cbox doctor    # verifica todo el setup
```

Soportado: **macOS** (OrbStack recomendado, o Docker Desktop), **Linux**
(Docker o Podman — el instalador ajusta el UID del contenedor al tuyo para
que los permisos de los repos no se rompan), **Windows vía WSL2**. El
instalador es idempotente: re-ejecútalo tras un `git pull` para actualizar.

Runtime alternativo: `CBOX_RUNTIME=podman`.

## Uso diario

```bash
cd ~/mi-proyecto
cbox                        # claude interactivo, sandboxed
cbox yolo                   # sin prompts de permisos (la red sigue filtrada)
cbox -p "arregla los tests" # one-shot; args pasan directos a claude
cbox shell                  # bash dentro, para depurar
cbox ps                     # sesiones cbox corriendo ahora mismo
cbox envs                   # entornos existentes
cbox doctor                 # chequeo completo del setup
cbox update                 # actualiza claude + imagen base
```

**Varias sesiones a la vez:** sin límite — cada invocación es un contenedor
efímero con nombre único. Abre N terminales, N proyectos, N `cbox`.

## Entornos dedicados: `@nombre`

Cada entorno tiene su propio volumen de config (login, historial, settings):

```bash
cbox @work          # usa el volumen claude-box-config-work
cbox @personal yolo
cbox @work login    # login solo para ese entorno
```

Sin `@`, usa el entorno default (`claude-box-config`). También se puede fijar
por proyecto con `CBOX_ENV=work` en `.cbox.conf`.

## Login

Primera vez por entorno: `cbox login` (o simplemente `cbox` — lo pedirá).
Dentro del contenedor no hay navegador, así que Claude muestra una **URL para
abrir en tu navegador del host** y te pide **pegar el código** de vuelta. Ese
flujo funciona siempre en contenedores; el de redirect automático no (el
callback apunta al localhost del contenedor).

Si algún día el flujo de pegar código fallara, plan B garantizado:

```bash
claude setup-token            # en el host, genera token de larga duración
export CLAUDE_CODE_OAUTH_TOKEN=...   # cbox lo pasa al contenedor automáticamente
```

El login queda persistido en el volumen del entorno — una vez y listo.

## Red (CBOX_NET)

| Modo | Internet | Host + LAN | Uso |
|---|---|---|---|
| `open` (default) | ✅ todo (docs, webs, APIs) | ❌ bloqueado por iptables | día a día y yolo |
| `allowlist` | solo `CBOX_ALLOWED_DOMAINS` | ❌ | paranoia / desatendido largo |
| `full` | ✅ | ✅ | solo si necesitas un servicio local |

`open` bloquea `host.docker.internal`, todos los rangos privados
(10/8, 172.16/12, 192.168/16), link-local, CGN y el rango de OrbStack. O sea:
Claude puede leer documentación en internet pero no puede tocar tu Mac ni
nada de tu red local. DNS queda abierto (necesario para resolver), así que
esto frena acceso al host y exfiltración casual, no un túnel DNS dedicado.

## Config por proyecto: `.cbox.conf`

```bash
CBOX_PORTS="3000 5173"                # dev servers publicados en 127.0.0.1
CBOX_MOUNTS="$HOME/datasets:/data:ro"
CBOX_NET="allowlist"
CBOX_ALLOWED_DOMAINS="api.anthropic.com,github.com,registry.npmjs.org"
CBOX_MEMORY="12g"
CBOX_SSH="agent"                      # key | agent | none
CBOX_ENV="work"
```

Ojo: si dos sesiones del mismo proyecto publican los mismos puertos, la
segunda fallará por puerto ocupado — quita `CBOX_PORTS` en la segunda.

## Recursos

Los límites (`8g` RAM, 2000 pids, swap = RAM) son **techos, no reservas**:
diez sesiones ociosas consumen casi nada; el límite solo actúa si un proceso
se desboca. La imagen se comparte entre todas las sesiones (una sola copia en
disco) y con OrbStack el arranque es <1s e I/O casi nativo. `CBOX_CPUS=4`
si quieres acotar CPU también.

## Terminal

`TERM` y `COLORTERM` se pasan del host, locale UTF-8 en la imagen, TTY real:
colores, resize y atajos funcionan como en local. El hostname del contenedor
es `cbox-<proyecto>` para que sepas dónde estás si abres `cbox shell`.

## SSH

- `CBOX_SSH=key` (default): monta `~/.ssh/claude_agent` **read-only**
  (`ssh-keygen -t ed25519 -f ~/.ssh/claude_agent`). Tu `~/.ssh` real no existe
  dentro del contenedor.
- `CBOX_SSH=agent`: reenvía el socket del ssh-agent (Docker Desktop/OrbStack);
  la llave privada **nunca entra** al contenedor. Combínalo con `ssh-add -c` o
  Secretive (Secure Enclave) para confirmar cada uso con Touch ID. Usa un
  agent dedicado: expone todas las llaves cargadas en él.
- Identidad git del agente: `~/.config/cbox/gitconfig` → `.gitconfig` si existe.

## Garantías por perfil

| | `cbox` | `cbox yolo` |
|---|---|---|
| Filesystem del host | solo `$PWD` | solo `$PWD` |
| Capabilities | solo las 5 del firewall | igual |
| Permisos de Claude | prompts normales | sin prompts |
| Red | internet sí, host/LAN no | igual (o `allowlist` si lo pides) |
