import { Paper, Group, Text, Badge, Stack, Box, UnstyledButton } from '@mantine/core';
import { IconFileText } from '@tabler/icons-react';
import { useNavigate } from 'react-router-dom';
import { Hit, HitSource } from '../../api';

export interface SearchResultCardProps {
  hit: Hit;
}

const sourceColors: Record<HitSource, string> = {
  bm25: 'blue',
  vec: 'grape',
  rerank: 'teal',
};

export function SearchResultCard({ hit }: SearchResultCardProps) {
  const navigate = useNavigate();

  const handleClick = () => {
    navigate(`/c/${hit.context}/m/${hit.doc_id}`);
  };

  return (
    <UnstyledButton
      onClick={handleClick}
      style={{
        display: 'block',
        width: '100%',
        textAlign: 'left',
      }}
    >
      <Paper
        withBorder
        p="sm"
        radius="md"
        style={{
          transition: 'border-color 0.15s ease',
          width: '100%',
        }}
      >
        <Stack gap={6}>
          <Group justify="space-between" align="flex-start" wrap="wrap">
            <Group gap={8} wrap="nowrap" style={{ minWidth: 0 }}>
              <IconFileText size={18} stroke={1.5} color="var(--mantine-color-accent-6)" />
              <Text
                size="sm"
                fw={600}
                style={{ fontFamily: 'ui-monospace, monospace' }}
                truncate
              >
                {hit.name || `${hit.doc_id}.md`}
              </Text>
              <Badge size="xs" variant="outline" color="gray">
                {hit.context}
              </Badge>
            </Group>

            <Group gap={6} wrap="nowrap">
              {hit.sources.map((src) => (
                <Badge
                  key={src}
                  size="xs"
                  variant="dot"
                  color={sourceColors[src] || 'gray'}
                >
                  {src}
                </Badge>
              ))}
              <Badge size="xs" variant="light" color="accent">
                {(hit.score * 100).toFixed(0)}%
              </Badge>
            </Group>
          </Group>

          {hit.heading && (
            <Text size="xs" fw={600} c="dimmed">
              § {hit.heading}
            </Text>
          )}

          <Box
            p="xs"
            bg="var(--mantine-color-body)"
            style={{
              borderLeft: '2px solid var(--mantine-color-accent-6)',
              borderRadius: '2px',
            }}
          >
            <Text
              size="xs"
              c="dimmed"
              style={{
                fontFamily: 'ui-monospace, monospace',
                whiteSpace: 'pre-wrap',
              }}
            >
              {hit.snippet}
            </Text>
          </Box>
        </Stack>
      </Paper>
    </UnstyledButton>
  );
}
