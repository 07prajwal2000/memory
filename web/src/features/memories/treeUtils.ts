import { MemorySummary } from '../../api';

export interface TreeFileNode {
  type: 'file';
  name: string;
  path: string;
  memory: MemorySummary;
}

export interface TreeFolderNode {
  type: 'folder';
  name: string;
  path: string;
  children: (TreeFolderNode | TreeFileNode)[];
}

export type TreeNode = TreeFolderNode | TreeFileNode;

export function buildMemoryTree(memories: MemorySummary[]): TreeNode[] {
  const rootFolders = new Map<string, TreeFolderNode>();
  const rootFiles: TreeFileNode[] = [];

  for (const memory of memories) {
    const rawName = memory.name || `${memory.id}.md`;
    const parts = rawName.split('/');

    if (parts.length === 1) {
      rootFiles.push({
        type: 'file',
        name: parts[0],
        path: rawName,
        memory,
      });
      continue;
    }

    const currentFolderMap = rootFolders;
    let currentPath = '';

    for (let i = 0; i < parts.length - 1; i++) {
      const folderName = parts[i];
      currentPath = currentPath ? `${currentPath}/${folderName}` : folderName;

      let folder = currentFolderMap.get(folderName);
      if (!folder) {
        folder = {
          type: 'folder',
          name: folderName,
          path: currentPath,
          children: [],
        };
        currentFolderMap.set(folderName, folder);
      }

      // Next level children
      if (i === parts.length - 2) {
        const fileName = parts[parts.length - 1];
        folder.children.push({
          type: 'file',
          name: fileName,
          path: rawName,
          memory,
        });
      }
    }
  }

  // Convert folder map to sorted array
  function sortNodes(nodes: TreeNode[]): TreeNode[] {
    return nodes.sort((a, b) => {
      if (a.type !== b.type) {
        return a.type === 'folder' ? -1 : 1;
      }
      return a.name.localeCompare(b.name);
    });
  }

  const result: TreeNode[] = [...Array.from(rootFolders.values()), ...rootFiles];
  return sortNodes(result);
}
