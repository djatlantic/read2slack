Need to build with this command to reduce size of the binary: go build -ldflags="-s -w"

read2slack will pipe outputs from command lines or tail a file content to a Slack channel.  read2slack also does rate limiting and buffering to avoid Slack disabling the webhook

Requirement:  One of the 3 config files must exist.  read2slack will read and use the first found info in the config file
1. /etc/slackchannels.toml
2. $HOME/.slackchannels.toml
3. $CWD/.slackchannels.toml (in the current directory where read2slack is invoked)

Sample config file:
----


Title = "Slack channels with incoming webhook"

[user]
icon = ":ghost"
name = "Ansible"
default_channel = "aws_bootstrap"     

[channels]

  [channels.aws_bootstrap]
  url = "https://hooks.slack.com/services/xxxxxxxx"
  channel = "#aws_bootstrap"

  [channels.bot_test]
  url = "https://hooks.slack.com/services/xxxxxxxxx"
  channel = "#bot_test"
---

Note about the config file:  
a. user.default_channel can be set to change the channel but without this read2slack will use "chatops" as the default channel
b. you can specify the channel with "-c/--channel" but the channel name is without '#' such as '-c chatops' or '--channel chatops'.  This option will override Default in config file and "chatops" default 
c. You can define multiple channels
d. user.name can be set to "" or not listed and the script will default to username@hostname


Usage examples:
1. Interactive output to slack channel
On 1st terminal: script -f outputfile
On 2nd terminal: tailf outputfile | read2slack
On 1st terminal: execute commands and output will be piped to slack.  End the session with exit command to quit out of script

2. Pipe the output to slack 
df | read2slack
df | read2slack -c channel_name

3. Quick posting to slack
read2slack -c channel_name Hello from Redwood City
read2slack data preparation 

