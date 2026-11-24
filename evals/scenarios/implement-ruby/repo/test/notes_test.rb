# frozen_string_literal: true

require 'minitest/autorun'
require 'tmpdir'
require 'notes'

class NotesTest < Minitest::Test
  def test_names_are_sorted
    Dir.mktmpdir do |dir|
      notes = Notes.new(dir)
      notes.save('b', 'second')
      notes.save('a', 'first')

      assert_equal %w[a b], notes.names
    end
  end
end
