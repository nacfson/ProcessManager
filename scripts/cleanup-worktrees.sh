#!/bin/bash
set -euo pipefail

# Ensure we are in the main project directory and on the main branch
CURRENT_BRANCH=$(git branch --show-current)
if [ "$CURRENT_BRANCH" != "main" ]; then
    echo "Error: You must be on the 'main' branch to run this cleanup script."
    exit 1
fi

echo "Scanning git worktrees..."

# Get all worktrees list, format is: <path> <commit> [<branch>]
# We read line by line.
git worktree list --porcelain | while read -r line; do
    if [[ "$line" =~ ^worktree\ (.*)$ ]]; then
        WT_PATH="${BASH_REMATCH[1]}"
    elif [[ "$line" =~ ^branch\ refs/heads/(.*)$ ]]; then
        WT_BRANCH="${BASH_REMATCH[1]}"
        
        # Skip main branch worktree
        if [ "$WT_BRANCH" = "main" ]; then
            continue
        fi

        echo "------------------------------------------------"
        echo "Processing worktree: $WT_PATH"
        echo "Associated branch: $WT_BRANCH"

        # Check if branch is merged into main
        if git branch --merged main | grep -qE "^\+?[[:space:]]*${WT_BRANCH}$"; then
            echo "Branch $WT_BRANCH is merged into main. Removing worktree..."
            if git worktree remove "$WT_PATH"; then
                echo "Successfully removed worktree at $WT_PATH"
                echo "Deleting branch $WT_BRANCH..."
                if git branch -d "$WT_BRANCH"; then
                    echo "Successfully deleted branch $WT_BRANCH"
                else
                    echo "Warning: Failed to delete branch $WT_BRANCH using 'git branch -d'. It may need force deletion."
                fi
            else
                echo "Warning: Failed to remove worktree at $WT_PATH. It might have uncommitted changes."
            fi
        else
            echo "Skipping branch $WT_BRANCH: not fully merged into main."
        fi
    fi
done

# Finally, prune worktrees metadata
echo "------------------------------------------------"
echo "Pruning worktrees metadata..."
git worktree prune
echo "Cleanup completed successfully!"
