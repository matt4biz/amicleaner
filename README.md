# amicleaner
This tool finds and removes orphaned AMIs and their snapshots

It finds all AMIs and identifies those not bound to an
instance that's still in use or to a launch template.

It then finds snapshots not bound to a volume used by
an instance that's in use.

Options:

```
  -dry-run
    	dry-run (list only) (default true)
  -region string
    	AWS region to search (default "us-east-2")
  -verbose
    	verbose output
```

The tool expects credentials in the usual way for the
AWS CLI.

## Method

1. Find all the instances, their image IDs and volumes
2. Check the instance state and ignore if in use
3. Find all launch templates and their images
4. List all AMIs in the account and region
5. Remove from that list those tied to an instance or a launch template
6. List all snapshots in the account and region
7. Remove from the list those tied to an instance's volume (through a block device mapping)
