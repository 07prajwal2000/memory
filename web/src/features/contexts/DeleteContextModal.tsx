import { notifications } from '@mantine/notifications';
import { useNavigate } from 'react-router-dom';
import { Context, useDeleteContext } from '../../api';
import { ConfirmModal } from '../../components';

export interface DeleteContextModalProps {
  context: Context | null;
  opened: boolean;
  onClose: () => void;
}

export function DeleteContextModal({ context, opened, onClose }: DeleteContextModalProps) {
  const deleteMutation = useDeleteContext();
  const navigate = useNavigate();

  if (!context) return null;

  const handleDelete = async () => {
    try {
      await deleteMutation.mutateAsync(context.slug);
      notifications.show({
        title: 'Context deleted',
        message: `Context "${context.slug}" and all its memories were permanently removed.`,
        color: 'blue',
      });
      onClose();
      navigate('/c/global');
    } catch (err: unknown) {
      notifications.show({
        title: 'Delete failed',
        // eslint-disable-next-line @typescript-eslint/no-explicit-any
        message: (err as any)?.message || 'Failed to delete context',
        color: 'red',
      });
    }
  };

  return (
    <ConfirmModal
      opened={opened}
      onClose={onClose}
      onConfirm={handleDelete}
      title="Delete Context"
      message={`Are you sure you want to delete context "${context.slug}"? All ${context.memory_count} memory document(s) in this context will be permanently erased.`}
      confirmLabel="Delete Context"
      requireMatchString={context.slug}
      requireMatchPlaceholder={`Type "${context.slug}" here`}
      isLoading={deleteMutation.isPending}
      color="red"
    />
  );
}
