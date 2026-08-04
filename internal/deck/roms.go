package deck

import (
	"context"
	"fmt"
	"path"
	"sort"
)

type ROM struct {
	Name string `json:"name"`
	Path string `json:"path"`
	Size int64  `json:"size"`
}

func (c *Client) ListROMs(ctx context.Context, system string) ([]ROM, error) {
	romsDir, systems := c.romSystems(ctx)
	if romsDir == "" {
		return nil, fmt.Errorf("no se encontró la carpeta de ROMs de EmuDeck")
	}

	validSystem := false
	for _, s := range systems {
		if s == system {
			validSystem = true
			break
		}
	}
	if !validSystem {
		return nil, fmt.Errorf("sistema no encontrado")
	}

	systemPath := path.Join(romsDir, system)
	entries, err := c.sftp.ReadDir(systemPath)
	if err != nil {
		return nil, err
	}

	var roms []ROM
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		roms = append(roms, ROM{
			Name: e.Name(),
			Path: path.Join(systemPath, e.Name()),
			Size: e.Size(),
		})
	}

	sort.Slice(roms, func(i, j int) bool {
		return roms[i].Name < roms[j].Name
	})

	return roms, nil
}

func (c *Client) DeleteROM(ctx context.Context, romPath string) error {
	romsDir, _ := c.romSystems(ctx)
	if romsDir == "" {
		return fmt.Errorf("no se encontró EmuDeck")
	}
	return c.sftp.Remove(romPath)
}

func (c *Client) RenameROM(ctx context.Context, oldPath, newName string) error {
	dir := path.Dir(oldPath)
	newPath := path.Join(dir, newName)
	return c.sftp.Rename(oldPath, newPath)
}

func (c *Client) DownloadROM(ctx context.Context, url string, system string) error {
	romsDir, _ := c.romSystems(ctx)
	if romsDir == "" {
		return fmt.Errorf("no se encontró EmuDeck")
	}

	systemPath := path.Join(romsDir, system)

	// wget -q -N para descargar
	cmd := fmt.Sprintf("wget -q -P %s %s", ShellQuote(systemPath), ShellQuote(url))
	_, err := c.Run(ctx, cmd)
	return err
}
