## marvel config

Manage marvel cluster configuration

### Synopsis

Manage marvel cluster configuration

### Options inherited from parent commands

```
      --cluster string       named cluster from ~/.marvel/config.yaml
  -i, --identity string      private key file for SSH auth (overrides cluster identity)
      --marvel-home string   marvel's state root, an absolute path (default ~/.marvel); sets $MARVEL_HOME, and the flag wins over the variable
      --socket string        explicit daemon address (overrides --cluster)
```

### SEE ALSO

* [marvel](marvel.md)	 - Agent orchestration control plane
* [marvel config add-cluster](marvel_config_add-cluster.md)	 - Add or update a cluster
* [marvel config current](marvel_config_current.md)	 - Show the current cluster
* [marvel config list](marvel_config_list.md)	 - List configured clusters
* [marvel config remove-cluster](marvel_config_remove-cluster.md)	 - Remove a cluster
* [marvel config repoint](marvel_config_repoint.md)	 - Move a cluster to a new address, carrying its host key trust over when the key matches
* [marvel config tmux-server](marvel_config_tmux-server.md)	 - Print the tmux server name this HOME's daemon uses
* [marvel config use-cluster](marvel_config_use-cluster.md)	 - Set the current cluster
