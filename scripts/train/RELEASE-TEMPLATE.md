# filex {{version}} - the train of {{date}}

| | |
|---|---|
| Release | {{tag}} - {{kind}} |
| Cut | {{cut}} Istanbul ({{cutUtc}} UTC) - {{cutState}} |
| Main at the cut | {{cutSha}} |
| Last release | {{prevTag}} ({{prevSha}}) |
| Profile | `pnpm release {{version}} --profile {{profile}}` |

## The rule this train runs by

{{trainRule}}

_From docs/CONTRIBUTING.md, Release process ("When"). The rule is changed
there, never here: this note is generated from it._

## On this train: main since {{prevTag}}, up to the cut

{{onTrain}}

## Arrived after the cut: the next train takes these

{{afterCut}}

## Merge queue

The branches still to merge for this train, in order, one per line with its
message file. A branch that arrives after the cut is not added: it rides the
next train. Run it with

    node scripts/train/merge-queue.mjs --queue <this file> --onto main --plan
    node scripts/train/merge-queue.mjs --queue <this file> --onto main

```queue
# <branch>  [<message file>]
```

## Steps

- [ ] Merge queue: every branch in, every check green
- [ ] `pnpm release {{version}} --plan`, then `pnpm release {{version}} --profile {{profile}}`
- [ ] README, screenshots and documentation audit, then `--resume --ack audit`
- [ ] Land, the GitHub gate green, signed tags pushed (`pnpm release {{version}} --resume`)
- [ ] `bash scripts/train/filex-ship.sh {{version}}` - every step green
- [ ] What the ship leaves to a person: the replies, the language packs, the embeds if it does not deploy them
- [ ] `pnpm release {{version}} --resume --ack deploy`

A long step wakes you when it ends instead of being watched:
`node scripts/train/when-done.mjs --title "<step> {{version}}" -- <command>`.

## Closing

- [ ] Every task on this train is moved to Done, in the same turn, each with its evidence (commit, version, measurement): {{tasks}}
- [ ] The release is written up where the maintainers keep their notes
