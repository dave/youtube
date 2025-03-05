# oracle vm

141.147.116.40
username: opc

## configure ssh

```chmod 600 ~/.ssh/oracle-ssh-key.key```

## ssh into oracle vm
```ssh -i ~/.ssh/oracle-ssh-key.key opc@141.147.116.40```

# keys

## google-service-account-token.json
Create here: https://console.cloud.google.com/iam-admin/serviceaccounts/details/104677990570467761179/keys?inv=1&invt=AbqgZw&project=wildernessprime&supportedpurview=project

## youtube-oauth2-client-secret.json
Create here: https://console.cloud.google.com/auth/clients?inv=1&invt=AbqgZQ&project=wildernessprime

youtube-oauth2-refresh-token.json is created automatically by the oauth login script the first time it runs.

## oracle-ssh-key.key 
Create here: [Oracle VM](oracle.md).

oracle-ssh-key.key.pub is the public key.