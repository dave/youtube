# Wilderness Prime YouTube Uploader

*This tool is not intended for general use.*

This tool uploads videos to YouTube:

- Titles and descriptions are generated by data and Go templates in a Google Sheet.
- Videos are uploaded from a Dropbox or Google Drive folder.
- Changes can be previewed before uploading, with diffs shown in the Google Sheet.
- Thumbnails are generated automatically.
- Uploads are resumed if the tool is interrupted.

I use this tool to upload all videos to the [Wilderness Prime YouTube channel](https://www.youtube.com/wildernessprime).

# Google Sheet containing data and templates

https://docs.google.com/spreadsheets/d/1e2gK0GgWN4PxeZcazUvxtlhYGzg2lZsZEkphqu9Jplc/edit?usp=sharing

# Oracle VM

Oracle gives out free VMs, so that's what I've been using to run the tool. 

[This is how to configure it](oracle.md)

## Oracle VM notes

```ssh -i ~/.ssh/oracle-ssh-key.key ubuntu@132.226.215.4```

- **New tmux session**: `tmux new -s mysession`
- **Detach**: `Ctrl + B`, then `D`
- **Reconnect** later: `tmux attach -t mysession`

# Keys

This tool uses three keys, which you will need to copy into `~/.config/wildernessprime/`.

## google-service-account-token.json
This is the service account key for authenticating with Google Sheets and Google Drive.

Create here: https://console.cloud.google.com/iam-admin/serviceaccounts/details/104677990570467761179/keys?inv=1&invt=AbqgZw&project=wildernessprime&supportedpurview=project

## youtube-oauth2-client-secret.json
This is the OAuth2 client secret for authenticating with YouTube.

Create here: https://console.cloud.google.com/auth/clients?inv=1&invt=AbqgZQ&project=wildernessprime

Refresh token `youtube-oauth2-refresh-token.json` is created automatically by the oauth login script the first time it runs (this won't work in an SSH terminal so you'll need to first run on your desktop and copy the refresh token by hand onto the server). If the Youtube API misbehaves, try deleting this file to force the oauth2 login flow to re-run.

## dropbox-oauth-client-id.txt, dropbox-oauth-client-secret.txt
These are needed by the Dropbox API to start the oauth2 login flow. To create them, you need to create a Dropbox app:

https://www.dropbox.com/developers/apps/create

Add permissions:
- files.metadata.write
- files.metadata.read
- files.content.write
- files.content.read
- sharing.read

Copy the two keys:
- App key = client-id
- App secret = client-secret

When the app first runs it will prompt you to log in and copy+paste an authorization code. It will them create two more files:

- dropbox-oauth-access-token.txt
- dropbox-oauth-refresh-token.txt

These are used to authenticate with the Dropbox API. If the Dropbox API misbehaves, try deleting these two files to force the oauth2 login flow to re-run.

# Google service account

Share sheet and drive folder with: youtubescript@wildernessprime.iam.gserviceaccount.com
