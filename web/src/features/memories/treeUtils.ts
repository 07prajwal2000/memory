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
  const rootNodes: TreeNode[] = [];
  const folderMap = new Map<string, TreeFolderNode>();

  for (const memory of memories) {
    const rawName = memory.name || `${memory.id}.md`;
    const parts = rawName.split('/');

    if (parts.length === 1) {
      rootNodes.push({
        type: 'file',
        name: parts[0],
        path: rawName,
        memory,
      });
      continue;
    }

    let currentPath = '';
    for (let i = 0; i < parts.length - 1; i++) {
      const folderName = parts[i];
      const parentPath = currentPath;
      currentPath = currentPath ? `${currentPath}/${folderName}` : folderName;

      let folder = folderMap.get(currentPath);
      if (!folder) {
        folder = {
          type: 'folder',
          name: folderName,
          path: currentPath,
          children: [],
        };
        folderMap.set(currentPath, folder);

        if (parentPath) {
          const parentFolder = folderMap.get(parentPath);
          parentFolder?.children.push(folder);
        } else {
          rootNodes.push(folder);
        }
      }

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

  function sortNodes(nodes: TreeNode[]): TreeNode[] {
    for (const node of nodes) {
      if (node.type === 'folder') {
        node.children = sortNodes(node.children);
      }
    }
    return nodes.sort((a, b) => {
      if (a.type !== b.type) {
        return a.type === 'folder' ? -1 : 1;
      }
      return a.name.localeCompare(b.name);
    });
  }

  return sortNodes(rootNodes);
}

export function getAncestorFolderPaths(rawName: string): string[] {
  const parts = rawName.split('/');
  if (parts.length <= 1) return [];

  const ancestors: string[] = [];
  let current = '';
  for (let i = 0; i < parts.length - 1; i++) {
    current = current ? `${current}/${parts[i]}` : parts[i];
    ancestors.push(current);
  }
  return ancestors;
}

export function getAllFolderPaths(nodes: TreeNode[]): string[] {
  const paths: string[] = [];
  function collect(list: TreeNode[]) {
    for (const node of list) {
      if (node.type === 'folder') {
        paths.push(node.path);
        collect(node.children);
      }
    }
  }
  collect(nodes);
  return paths;
}
