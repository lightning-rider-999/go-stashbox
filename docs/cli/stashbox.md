## stashbox

Agent-first, read-only CLI for the stash-box GraphQL API

### Synopsis

stashbox is a machine-readable command-line client for a stash-box instance (StashDB and any other). Every stash-box query is exposed as a resource-and-verb command (e.g. `stashbox scene get`, `stashbox performer query`). It is read-only: there are no mutations, no async jobs, and no destructive confirmation gate. Output is JSON by default.

```
stashbox [flags]
```

### Options

```
      --api-key string   stash-box API key (default $STASHBOX_API_KEY)
  -h, --help             help for stashbox
      --input string     variables source: JSON file path, or "-" for stdin
  -o, --output string    output format: json, table (default "json")
      --url string       stash-box base URL (default $STASHBOX_URL)
```

### SEE ALSO

* [stashbox catalog](stashbox_catalog.md)	 - Print the embedded machine-facing operation catalog
* [stashbox config](stashbox_config.md)	 - config operations
* [stashbox draft](stashbox_draft.md)	 - draft operations
* [stashbox edit](stashbox_edit.md)	 - edit operations
* [stashbox misc](stashbox_misc.md)	 - misc operations
* [stashbox mod-audit](stashbox_mod-audit.md)	 - mod-audit operations
* [stashbox notification](stashbox_notification.md)	 - notification operations
* [stashbox performer](stashbox_performer.md)	 - performer operations
* [stashbox scene](stashbox_scene.md)	 - scene operations
* [stashbox site](stashbox_site.md)	 - site operations
* [stashbox site-category](stashbox_site-category.md)	 - site-category operations
* [stashbox studio](stashbox_studio.md)	 - studio operations
* [stashbox tag](stashbox_tag.md)	 - tag operations
* [stashbox tag-category](stashbox_tag-category.md)	 - tag-category operations
* [stashbox user](stashbox_user.md)	 - user operations

