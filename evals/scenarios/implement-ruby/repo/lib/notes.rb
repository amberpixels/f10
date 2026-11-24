# frozen_string_literal: true

# Notes keeps text notes as <name>.txt files in one directory.
class Notes
  def initialize(dir)
    @dir = dir
  end

  def save(name, text)
    File.write(path(name), text)
  end

  # names lists the notes, sorted.
  def names
    Dir.glob(File.join(@dir, '*.txt')).map { |f| File.basename(f, '.txt') }.sort
  end

  private

  def path(name)
    File.join(@dir, "#{name}.txt")
  end
end
