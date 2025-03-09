# Data sheet

https://docs.google.com/spreadsheets/d/1e2gK0GgWN4PxeZcazUvxtlhYGzg2lZsZEkphqu9Jplc/edit?usp=sharing

# oracle vm

Oracle gives out free VMs, so that's what I've been using. [This is how to configure it](oracle.md)

```ssh -i ~/.ssh/oracle-ssh-key.key ubuntu@132.226.215.4```

- **New tmux session**: `tmux new -s mysession`
- **Detach**: `Ctrl + B`, then `D`
- **Reconnect** later: `tmux attach -t mysession`

# keys

## google-service-account-token.json
Create here: https://console.cloud.google.com/iam-admin/serviceaccounts/details/104677990570467761179/keys?inv=1&invt=AbqgZw&project=wildernessprime&supportedpurview=project

## youtube-oauth2-client-secret.json
Create here: https://console.cloud.google.com/auth/clients?inv=1&invt=AbqgZQ&project=wildernessprime

Refresh token `youtube-oauth2-refresh-token.json` is created automatically by the oauth login script the first time it runs.

## oracle-ssh-key.key 
Create here: [Oracle VM](oracle.md).

Public key: `oracle-ssh-key.key.pub`.

- **Configure SSH key**: `chmod 600 ~/.ssh/oracle-ssh-key.key`