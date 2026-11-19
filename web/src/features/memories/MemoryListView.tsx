import { useParams } from 'react-router-dom';
import { MemoryPanel } from './MemoryPanel';

export function MemoryListView() {
  const { slug = 'global', id } = useParams<{ slug?: string; id?: string }>();
  return <MemoryPanel slug={slug} activeId={id} />;
}
